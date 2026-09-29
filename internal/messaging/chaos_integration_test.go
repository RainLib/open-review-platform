package messaging

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/RainLib/open-review-platform/internal/domain"
	"github.com/RainLib/open-review-platform/internal/store"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	amqp "github.com/rabbitmq/amqp091-go"
)

// TestAMQPInboxRecoversAcrossBrokerAndDatabasePartitions exercises the two
// network boundaries around the inbox fence with real PostgreSQL and RabbitMQ:
//
//  1. PostgreSQL commits the inbox/effect, then the broker connection is cut
//     before ACK. RabbitMQ redelivers after healing, while the completed inbox
//     suppresses a second effect.
//  2. PostgreSQL is unreachable before an inbox claim. The handler must not
//     run; after healing, the requeued delivery is claimed and executed once.
//
// The proxies are deliberately in-process and transport-only. They establish
// the application recovery contract without claiming to reproduce a specific
// Kubernetes CNI, cloud load balancer, or production network policy.
func TestAMQPInboxRecoversAcrossBrokerAndDatabasePartitions(t *testing.T) {
	databaseURL := os.Getenv("OPEN_REVIEW_TEST_DATABASE_URL")
	brokerURL := os.Getenv("OPEN_REVIEW_TEST_AMQP_URL")
	if databaseURL == "" || brokerURL == "" {
		t.Skip("OPEN_REVIEW_TEST_DATABASE_URL and OPEN_REVIEW_TEST_AMQP_URL are required")
	}
	if os.Getenv("OPEN_REVIEW_TEST_ISOLATED_DATABASE") != "true" {
		t.Skip("network partition verification requires an isolated database")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	database, err := store.Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err := pool.Exec(ctx, `
		CREATE TABLE IF NOT EXISTS messaging_chaos_effects (
			message_id UUID PRIMARY KEY,
			worker_id TEXT NOT NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)`); err != nil {
		t.Fatalf("create isolated chaos effect ledger: %v", err)
	}
	defer func() { _, _ = pool.Exec(context.Background(), `DROP TABLE messaging_chaos_effects`) }()

	suffix := uuid.NewString()
	exchange := "openreview.test.network-partition." + suffix
	queue := "openreview.test.network-partition." + suffix
	routingKey := "review.test.network-partition." + suffix
	consumer := "network-partition-fence-" + suffix
	publisher, err := OpenAMQPPublisher(brokerURL, exchange)
	if err != nil {
		t.Fatal(err)
	}
	defer publisher.Close()
	setupConnection, setupChannel := openChaosChannel(t, brokerURL)
	if _, err := setupChannel.QueueDeclare(queue, false, false, false, false, nil); err != nil {
		t.Fatal(err)
	}
	if err := setupChannel.QueueBind(queue, routingKey, exchange, false, nil); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = setupChannel.QueueDelete(queue, false, false, false)
		_ = setupChannel.ExchangeDelete(exchange, false, false)
		_ = setupChannel.Close()
		_ = setupConnection.Close()
	}()

	brokerProxy, proxiedBrokerURL := newPartitionProxy(t, brokerURL)
	defer brokerProxy.Close()
	committedBeforeACK := domain.OutboxMessage{
		ID: uuid.New(), AggregateID: uuid.New(), Topic: routingKey,
		Payload: map[string]any{"window": "database-commit-before-broker-ack"},
	}
	if err := publisher.Publish(ctx, committedBeforeACK); err != nil {
		t.Fatalf("publish broker partition fixture: %v", err)
	}
	connection, channel, delivery := receiveChaosDelivery(t, ctx, proxiedBrokerURL, queue, "broker-partition-before-ack-"+suffix)
	closed := connection.NotifyClose(make(chan *amqp.Error, 1))
	message, err := DecodeOutboxMessage(delivery.Body)
	if err != nil {
		t.Fatal(err)
	}
	var firstExecutions atomic.Int32
	if err := HandleExactlyOnce(ctx, database, consumer, message, func(ctx context.Context, message domain.OutboxMessage) error {
		firstExecutions.Add(1)
		_, err := pool.Exec(ctx, `INSERT INTO messaging_chaos_effects (message_id,worker_id) VALUES ($1,$2)`, message.ID, "broker-partition")
		return err
	}); err != nil {
		t.Fatalf("complete inbox before broker partition: %v", err)
	}
	brokerProxy.Partition()
	select {
	case <-closed:
	case <-ctx.Done():
		t.Fatalf("broker connection did not close during partition: %v", ctx.Err())
	}
	if err := delivery.Ack(false); err == nil {
		t.Fatal("ACK unexpectedly succeeded after the broker transport was partitioned")
	}
	_ = channel.Close()
	_ = connection.Close()
	brokerProxy.Heal()

	recoveryConnection, recoveryChannel, redelivery := receiveChaosDelivery(t, ctx, proxiedBrokerURL, queue, "broker-partition-recovery-"+suffix)
	if !redelivery.Redelivered {
		t.Fatal("delivery after broker partition was not marked redelivered")
	}
	redeliveredMessage, err := DecodeOutboxMessage(redelivery.Body)
	if err != nil {
		t.Fatal(err)
	}
	if err := HandleExactlyOnce(ctx, database, consumer, redeliveredMessage, func(context.Context, domain.OutboxMessage) error {
		firstExecutions.Add(1)
		return nil
	}); err != nil {
		t.Fatalf("handle broker-partition redelivery: %v", err)
	}
	if err := redelivery.Ack(false); err != nil {
		t.Fatal(err)
	}
	_ = recoveryChannel.Close()
	_ = recoveryConnection.Close()
	if got := firstExecutions.Load(); got != 1 {
		t.Fatalf("handler executions across broker partition=%d, want 1", got)
	}
	assertInboxState(t, ctx, pool, consumer, committedBeforeACK.ID, "completed", 1)
	assertChaosEffectCount(t, ctx, pool, committedBeforeACK.ID, 1)

	databaseProxy, proxiedDatabaseURL := newPartitionProxy(t, databaseURL)
	defer databaseProxy.Close()
	partitionedStore, err := store.Open(ctx, proxiedDatabaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer partitionedStore.Close()
	partitionedPool, err := pgxpool.New(ctx, proxiedDatabaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer partitionedPool.Close()
	if err := partitionedPool.Ping(ctx); err != nil {
		t.Fatal(err)
	}

	unclaimedDuringPartition := domain.OutboxMessage{
		ID: uuid.New(), AggregateID: uuid.New(), Topic: routingKey,
		Payload: map[string]any{"window": "database-partition-before-inbox-claim"},
	}
	if err := publisher.Publish(ctx, unclaimedDuringPartition); err != nil {
		t.Fatalf("publish database partition fixture: %v", err)
	}
	databaseConnection, databaseChannel, databaseDelivery := receiveChaosDelivery(t, ctx, brokerURL, queue, "database-partition-before-claim-"+suffix)
	databaseMessage, err := DecodeOutboxMessage(databaseDelivery.Body)
	if err != nil {
		t.Fatal(err)
	}
	databaseProxy.Partition()
	var databaseExecutions atomic.Int32
	failedContext, failedCancel := context.WithTimeout(ctx, 2*time.Second)
	err = HandleExactlyOnce(failedContext, partitionedStore, consumer, databaseMessage, func(context.Context, domain.OutboxMessage) error {
		databaseExecutions.Add(1)
		return nil
	})
	failedCancel()
	if err == nil {
		t.Fatal("inbox claim unexpectedly succeeded while PostgreSQL was partitioned")
	}
	if got := databaseExecutions.Load(); got != 0 {
		t.Fatalf("handler executions while PostgreSQL was partitioned=%d, want 0", got)
	}
	if err := databaseDelivery.Nack(false, true); err != nil {
		t.Fatalf("requeue after database partition: %v", err)
	}
	_ = databaseChannel.Close()
	_ = databaseConnection.Close()
	databaseProxy.Heal()

	var recovered bool
	recoveryErrors := make([]string, 0, 3)
	for attempt := 1; attempt <= 3; attempt++ {
		healedConnection, healedChannel, healedDelivery := receiveChaosDelivery(t, ctx, brokerURL, queue, fmt.Sprintf("database-partition-recovery-%d-%s", attempt, suffix))
		healedMessage, err := DecodeOutboxMessage(healedDelivery.Body)
		if err != nil {
			t.Fatal(err)
		}
		err = HandleExactlyOnce(ctx, partitionedStore, consumer, healedMessage, func(ctx context.Context, message domain.OutboxMessage) error {
			command, err := partitionedPool.Exec(ctx, `INSERT INTO messaging_chaos_effects (message_id,worker_id) VALUES ($1,$2) ON CONFLICT (message_id) DO NOTHING`, message.ID, "database-partition")
			if err == nil && command.RowsAffected() == 1 {
				databaseExecutions.Add(1)
			}
			return err
		})
		if err != nil {
			recoveryErrors = append(recoveryErrors, err.Error())
			if nackErr := healedDelivery.Nack(false, true); nackErr != nil {
				t.Fatalf("requeue database recovery attempt %d: %v (handler: %v)", attempt, nackErr, err)
			}
			_ = healedChannel.Close()
			_ = healedConnection.Close()
			continue
		}
		if err := healedDelivery.Ack(false); err != nil {
			t.Fatal(err)
		}
		_ = healedChannel.Close()
		_ = healedConnection.Close()
		recovered = true
		break
	}
	if !recovered {
		t.Fatalf("database-backed consumer did not recover after three redeliveries: %v", recoveryErrors)
	}
	if got := databaseExecutions.Load(); got != 1 {
		t.Fatalf("handler executions across database partition=%d, want 1", got)
	}
	assertInboxCompletedWithinAttempts(t, ctx, pool, consumer, unclaimedDuringPartition.ID, 1, 4)
	assertChaosEffectCount(t, ctx, pool, unclaimedDuringPartition.ID, 1)

	if _, err := pool.Exec(ctx, `DELETE FROM inbox_messages WHERE consumer=$1`, consumer); err != nil {
		t.Fatalf("clean network partition inbox fixtures: %v", err)
	}
}

// TestAMQPInboxRecoversAcrossWorkerCrashesAndSuppressesRedelivery exercises
// the complete broker -> inbox fence with two independent worker transports.
// It covers both crash windows that matter to an at-least-once consumer:
//
//  1. a worker receives the delivery and dies before completing its inbox
//     claim; the delivery and expired database lease are reclaimed;
//  2. a worker completes the inbox transaction and dies before ACK; the
//     redelivery is ACKed without executing the handler again.
//
// The test is opt-in because it requires an isolated PostgreSQL database and a
// real RabbitMQ broker. It never uses the development database.
func TestAMQPInboxRecoversAcrossWorkerCrashesAndSuppressesRedelivery(t *testing.T) {
	databaseURL := os.Getenv("OPEN_REVIEW_TEST_DATABASE_URL")
	brokerURL := os.Getenv("OPEN_REVIEW_TEST_AMQP_URL")
	if databaseURL == "" || brokerURL == "" {
		t.Skip("OPEN_REVIEW_TEST_DATABASE_URL and OPEN_REVIEW_TEST_AMQP_URL are required")
	}
	if os.Getenv("OPEN_REVIEW_TEST_ISOLATED_DATABASE") != "true" {
		t.Skip("worker crash verification requires an isolated database")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	database, err := store.Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer database.Close()
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	defer pool.Close()
	if _, err := pool.Exec(ctx, `
		CREATE TABLE messaging_chaos_effects (
			message_id UUID PRIMARY KEY,
			worker_id TEXT NOT NULL,
			created_at TIMESTAMPTZ NOT NULL DEFAULT now()
		)`); err != nil {
		t.Fatalf("create isolated chaos effect ledger: %v", err)
	}
	defer func() { _, _ = pool.Exec(context.Background(), `DROP TABLE messaging_chaos_effects`) }()

	suffix := uuid.NewString()
	exchange := "openreview.test.worker-crash." + suffix
	queue := "openreview.test.worker-crash." + suffix
	routingKey := "review.test.worker-crash." + suffix
	consumer := "worker-crash-fence-" + suffix
	publisher, err := OpenAMQPPublisher(brokerURL, exchange)
	if err != nil {
		t.Fatal(err)
	}
	defer publisher.Close()

	setupConnection, setupChannel := openChaosChannel(t, brokerURL)
	if _, err := setupChannel.QueueDeclare(queue, false, false, false, false, nil); err != nil {
		t.Fatal(err)
	}
	if err := setupChannel.QueueBind(queue, routingKey, exchange, false, nil); err != nil {
		t.Fatal(err)
	}
	defer func() {
		_, _ = setupChannel.QueueDelete(queue, false, false, false)
		_ = setupChannel.ExchangeDelete(exchange, false, false)
		_ = setupChannel.Close()
		_ = setupConnection.Close()
	}()

	beforeCompletion := domain.OutboxMessage{
		ID: uuid.New(), AggregateID: uuid.New(), Topic: routingKey,
		Payload: map[string]any{"window": "before-inbox-completion"},
	}
	if err := publisher.Publish(ctx, beforeCompletion); err != nil {
		t.Fatalf("publish before-completion fixture: %v", err)
	}

	// Worker A is a separate OS process. It exits while holding the delivery and
	// database claim, so neither Go defers nor an explicit NACK can assist the
	// recovery path.
	runChaosWorkerProcess(t, ctx, "claim", databaseURL, brokerURL, queue, consumer)
	if _, err := pool.Exec(ctx, `
		UPDATE inbox_messages
		SET locked_until = now() - interval '1 second'
		WHERE consumer=$1 AND message_id=$2 AND state='claimed'`, consumer, beforeCompletion.ID); err != nil {
		t.Fatalf("expire crashed worker inbox lease: %v", err)
	}

	workerBConnection, workerBChannel, reclaimed := receiveChaosDelivery(t, ctx, brokerURL, queue, "worker-b-reclaim-"+suffix)
	if !reclaimed.Redelivered {
		t.Fatal("delivery reclaimed after worker crash was not marked redelivered")
	}
	reclaimedMessage, err := DecodeOutboxMessage(reclaimed.Body)
	if err != nil {
		t.Fatal(err)
	}
	var reclaimedExecutions atomic.Int32
	if err := HandleExactlyOnce(ctx, database, consumer, reclaimedMessage, func(context.Context, domain.OutboxMessage) error {
		reclaimedExecutions.Add(1)
		return nil
	}); err != nil {
		t.Fatalf("worker B reclaim handler: %v", err)
	}
	if err := reclaimed.Ack(false); err != nil {
		t.Fatal(err)
	}
	_ = workerBChannel.Close()
	_ = workerBConnection.Close()

	// A second physical copy with the same immutable message ID models the
	// publish-confirm uncertainty window. A completed inbox row must suppress it.
	if err := publisher.Publish(ctx, beforeCompletion); err != nil {
		t.Fatalf("republish duplicate fixture: %v", err)
	}
	duplicateConnection, duplicateChannel, duplicate := receiveChaosDelivery(t, ctx, brokerURL, queue, "worker-c-duplicate-"+suffix)
	duplicateMessage, err := DecodeOutboxMessage(duplicate.Body)
	if err != nil {
		t.Fatal(err)
	}
	if err := HandleExactlyOnce(ctx, database, consumer, duplicateMessage, func(context.Context, domain.OutboxMessage) error {
		reclaimedExecutions.Add(1)
		return nil
	}); err != nil {
		t.Fatalf("completed duplicate handler: %v", err)
	}
	if err := duplicate.Ack(false); err != nil {
		t.Fatal(err)
	}
	_ = duplicateChannel.Close()
	_ = duplicateConnection.Close()
	if got := reclaimedExecutions.Load(); got != 1 {
		t.Fatalf("handler executions after crash and duplicate=%d, want 1", got)
	}
	assertInboxState(t, ctx, pool, consumer, beforeCompletion.ID, "completed", 2)

	afterCompletion := domain.OutboxMessage{
		ID: uuid.New(), AggregateID: uuid.New(), Topic: routingKey,
		Payload: map[string]any{"window": "after-inbox-completion-before-ack"},
	}
	if err := publisher.Publish(ctx, afterCompletion); err != nil {
		t.Fatalf("publish after-completion fixture: %v", err)
	}
	// Worker C commits its inbox receipt and one durable effect, then the child
	// process exits before sending the RabbitMQ ACK.
	runChaosWorkerProcess(t, ctx, "complete", databaseURL, brokerURL, queue, consumer)

	workerDConnection, workerDChannel, completedRedelivery := receiveChaosDelivery(t, ctx, brokerURL, queue, "worker-d-redelivery-"+suffix)
	if !completedRedelivery.Redelivered {
		t.Fatal("delivery after completion/ACK crash was not marked redelivered")
	}
	redeliveredMessage, err := DecodeOutboxMessage(completedRedelivery.Body)
	if err != nil {
		t.Fatal(err)
	}
	var completedExecutions atomic.Int32
	if err := HandleExactlyOnce(ctx, database, consumer, redeliveredMessage, func(context.Context, domain.OutboxMessage) error {
		completedExecutions.Add(1)
		return nil
	}); err != nil {
		t.Fatalf("worker D completed redelivery handler: %v", err)
	}
	if err := completedRedelivery.Ack(false); err != nil {
		t.Fatal(err)
	}
	_ = workerDChannel.Close()
	_ = workerDConnection.Close()
	if got := completedExecutions.Load(); got != 0 {
		t.Fatalf("parent handler executions after completion/ACK crash=%d, want 0", got)
	}
	assertInboxState(t, ctx, pool, consumer, afterCompletion.ID, "completed", 1)
	var effectCount int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM messaging_chaos_effects WHERE message_id=$1`, afterCompletion.ID).Scan(&effectCount); err != nil {
		t.Fatal(err)
	}
	if effectCount != 1 {
		t.Fatalf("durable effects after completion/ACK crash=%d, want 1", effectCount)
	}

	if _, err := pool.Exec(ctx, `DELETE FROM inbox_messages WHERE consumer=$1`, consumer); err != nil {
		t.Fatalf("clean inbox fixtures: %v", err)
	}
}

// TestAMQPChaosWorkerProcess is invoked only as a subprocess by the parent
// integration test. os.Exit deliberately bypasses cleanup so RabbitMQ observes
// an abrupt process/connection loss with an unacknowledged delivery.
func TestAMQPChaosWorkerProcess(t *testing.T) {
	mode := os.Getenv("OPEN_REVIEW_CHAOS_WORKER_MODE")
	if mode == "" {
		t.Skip("chaos worker subprocess only")
	}
	databaseURL := os.Getenv("OPEN_REVIEW_TEST_DATABASE_URL")
	brokerURL := os.Getenv("OPEN_REVIEW_TEST_AMQP_URL")
	queue := os.Getenv("OPEN_REVIEW_CHAOS_QUEUE")
	consumer := os.Getenv("OPEN_REVIEW_CHAOS_CONSUMER")
	if databaseURL == "" || brokerURL == "" || queue == "" || consumer == "" {
		t.Fatal("chaos worker environment is incomplete")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	database, err := store.Open(ctx, databaseURL)
	if err != nil {
		t.Fatal(err)
	}
	_, _, delivery := receiveChaosDelivery(t, ctx, brokerURL, queue, "subprocess-"+mode+"-"+uuid.NewString())
	message, err := DecodeOutboxMessage(delivery.Body)
	if err != nil {
		t.Fatal(err)
	}
	switch mode {
	case "claim":
		token, claimed, err := database.ClaimInbox(ctx, consumer, message.ID)
		if err != nil || !claimed || token == uuid.Nil {
			t.Fatalf("subprocess claim token=%s claimed=%t error=%v", token, claimed, err)
		}
	case "complete":
		pool, err := pgxpool.New(ctx, databaseURL)
		if err != nil {
			t.Fatal(err)
		}
		if err := HandleExactlyOnce(ctx, database, consumer, message, func(ctx context.Context, message domain.OutboxMessage) error {
			_, err := pool.Exec(ctx, `INSERT INTO messaging_chaos_effects (message_id,worker_id) VALUES ($1,$2)`, message.ID, "subprocess-complete")
			return err
		}); err != nil {
			t.Fatalf("subprocess completion handler: %v", err)
		}
	default:
		t.Fatalf("unknown chaos worker mode %q", mode)
	}
	os.Exit(0)
}

func runChaosWorkerProcess(t *testing.T, ctx context.Context, mode, databaseURL, brokerURL, queue, consumer string) {
	t.Helper()
	command := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestAMQPChaosWorkerProcess$", "-test.v")
	command.Env = append(os.Environ(),
		"OPEN_REVIEW_CHAOS_WORKER_MODE="+mode,
		"OPEN_REVIEW_TEST_DATABASE_URL="+databaseURL,
		"OPEN_REVIEW_TEST_AMQP_URL="+brokerURL,
		"OPEN_REVIEW_CHAOS_QUEUE="+queue,
		"OPEN_REVIEW_CHAOS_CONSUMER="+consumer,
	)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("chaos worker %s failed: %v\n%s", mode, err, output)
	}
}

func openChaosChannel(t *testing.T, brokerURL string) (*amqp.Connection, *amqp.Channel) {
	t.Helper()
	connection, err := amqp.Dial(brokerURL)
	if err != nil {
		t.Fatal(err)
	}
	channel, err := connection.Channel()
	if err != nil {
		_ = connection.Close()
		t.Fatal(err)
	}
	return connection, channel
}

func receiveChaosDelivery(t *testing.T, ctx context.Context, brokerURL, queue, consumer string) (*amqp.Connection, *amqp.Channel, amqp.Delivery) {
	t.Helper()
	connection, channel := openChaosChannel(t, brokerURL)
	if err := channel.Qos(1, 0, false); err != nil {
		_ = channel.Close()
		_ = connection.Close()
		t.Fatal(err)
	}
	deliveries, err := channel.Consume(queue, consumer, false, false, false, false, nil)
	if err != nil {
		_ = channel.Close()
		_ = connection.Close()
		t.Fatal(err)
	}
	select {
	case delivery, ok := <-deliveries:
		if !ok {
			t.Fatal("broker delivery channel closed")
		}
		return connection, channel, delivery
	case <-ctx.Done():
		t.Fatal(fmt.Sprintf("timed out waiting for %s: %v", consumer, ctx.Err()))
		return nil, nil, amqp.Delivery{}
	}
}

func assertInboxState(t *testing.T, ctx context.Context, pool *pgxpool.Pool, consumer string, messageID uuid.UUID, wantState string, wantAttempts int) {
	t.Helper()
	var state string
	var attempts int
	if err := pool.QueryRow(ctx, `
		SELECT state,attempt
		FROM inbox_messages
		WHERE consumer=$1 AND message_id=$2`, consumer, messageID).Scan(&state, &attempts); err != nil {
		t.Fatal(err)
	}
	if state != wantState || attempts != wantAttempts {
		t.Fatalf("inbox state=%s attempts=%d, want %s/%d", state, attempts, wantState, wantAttempts)
	}
}

func assertChaosEffectCount(t *testing.T, ctx context.Context, pool *pgxpool.Pool, messageID uuid.UUID, want int) {
	t.Helper()
	var count int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM messaging_chaos_effects WHERE message_id=$1`, messageID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != want {
		t.Fatalf("durable effects for %s=%d, want %d", messageID, count, want)
	}
}

func assertInboxCompletedWithinAttempts(t *testing.T, ctx context.Context, pool *pgxpool.Pool, consumer string, messageID uuid.UUID, minimum, maximum int) {
	t.Helper()
	var state string
	var attempts int
	if err := pool.QueryRow(ctx, `
		SELECT state,attempt
		FROM inbox_messages
		WHERE consumer=$1 AND message_id=$2`, consumer, messageID).Scan(&state, &attempts); err != nil {
		t.Fatal(err)
	}
	if state != "completed" || attempts < minimum || attempts > maximum {
		t.Fatalf("inbox state=%s attempts=%d, want completed with %d..%d attempts", state, attempts, minimum, maximum)
	}
}

type partitionProxy struct {
	listener net.Listener
	target   string
	done     chan struct{}

	mu          sync.Mutex
	partitioned bool
	connections map[net.Conn]struct{}
	closeOnce   sync.Once
}

func newPartitionProxy(t *testing.T, rawURL string) (*partitionProxy, string) {
	t.Helper()
	parsed, err := url.Parse(rawURL)
	if err != nil {
		t.Fatalf("parse partition target URL: %v", err)
	}
	port := parsed.Port()
	if port == "" {
		switch parsed.Scheme {
		case "amqp":
			port = "5672"
		case "amqps":
			port = "5671"
		case "postgres", "postgresql":
			port = "5432"
		default:
			t.Fatalf("partition target URL requires an explicit port for scheme %q", parsed.Scheme)
		}
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen for partition proxy: %v", err)
	}
	proxy := &partitionProxy{
		listener:    listener,
		target:      net.JoinHostPort(parsed.Hostname(), port),
		done:        make(chan struct{}),
		connections: make(map[net.Conn]struct{}),
	}
	parsed.Host = listener.Addr().String()
	go proxy.accept()
	return proxy, parsed.String()
}

func (proxy *partitionProxy) accept() {
	for {
		client, err := proxy.listener.Accept()
		if err != nil {
			select {
			case <-proxy.done:
				return
			default:
				continue
			}
		}
		go proxy.forward(client)
	}
}

func (proxy *partitionProxy) forward(client net.Conn) {
	proxy.mu.Lock()
	if proxy.partitioned {
		proxy.mu.Unlock()
		_ = client.Close()
		return
	}
	proxy.mu.Unlock()

	upstream, err := net.DialTimeout("tcp", proxy.target, 2*time.Second)
	if err != nil {
		_ = client.Close()
		return
	}
	proxy.mu.Lock()
	if proxy.partitioned {
		proxy.mu.Unlock()
		_ = client.Close()
		_ = upstream.Close()
		return
	}
	proxy.connections[client] = struct{}{}
	proxy.connections[upstream] = struct{}{}
	proxy.mu.Unlock()

	copyDone := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(upstream, client); copyDone <- struct{}{} }()
	go func() { _, _ = io.Copy(client, upstream); copyDone <- struct{}{} }()
	<-copyDone
	_ = client.Close()
	_ = upstream.Close()
	proxy.mu.Lock()
	delete(proxy.connections, client)
	delete(proxy.connections, upstream)
	proxy.mu.Unlock()
}

func (proxy *partitionProxy) Partition() {
	proxy.mu.Lock()
	proxy.partitioned = true
	for connection := range proxy.connections {
		_ = connection.Close()
	}
	proxy.mu.Unlock()
}

func (proxy *partitionProxy) Heal() {
	proxy.mu.Lock()
	proxy.partitioned = false
	proxy.mu.Unlock()
}

func (proxy *partitionProxy) Close() {
	proxy.closeOnce.Do(func() {
		close(proxy.done)
		_ = proxy.listener.Close()
		proxy.mu.Lock()
		for connection := range proxy.connections {
			_ = connection.Close()
		}
		proxy.mu.Unlock()
	})
}
