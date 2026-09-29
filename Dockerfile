# Pin an existing patch release so a local rebuild does not resolve a moving
# language tag before it can compile the current control-plane source.
FROM golang:1.25.14-alpine AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN --mount=type=cache,id=open-review-go-build-cache,target=/root/.cache/go-build \
	CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags='-s -w' -o /out/control-api ./cmd/control-api \
	&& CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags='-s -w' -o /out/agent-credential-broker ./cmd/agent-credential-broker \
	&& CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags='-s -w' -o /out/migrate ./cmd/migrate \
	&& CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags='-s -w' -o /out/outbox-relay ./cmd/outbox-relay \
	&& CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags='-s -w' -o /out/interaction-responder ./cmd/interaction-responder \
	&& CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags='-s -w' -o /out/interaction-admitter ./cmd/interaction-admitter \
	&& CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags='-s -w' -o /out/agent-task-source-admitter ./cmd/agent-task-source-admitter \
	&& CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags='-s -w' -o /out/acknowledger ./cmd/acknowledger \
	&& CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags='-s -w' -o /out/terminal-reporter ./cmd/terminal-reporter \
	&& CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags='-s -w' -o /out/issue-publisher ./cmd/issue-publisher \
	&& CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags='-s -w' -o /out/issue-triager ./cmd/issue-triager \
	&& CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags='-s -w' -o /out/notifier ./cmd/notifier \
	&& CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags='-s -w' -o /out/sso-prober ./cmd/sso-prober \
	&& CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags='-s -w' -o /out/provider-prober ./cmd/provider-prober \
	&& CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags='-s -w' -o /out/provider-feedback-poller ./cmd/provider-feedback-poller \
	&& CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags='-s -w' -o /out/model-prober ./cmd/model-prober \
	&& CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags='-s -w' -o /out/data-governance-worker ./cmd/data-governance-worker \
	&& CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags='-s -w' -o /out/review-scheduler ./cmd/review-scheduler \
	&& CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags='-s -w' -o /out/rule-exception-expirer ./cmd/rule-exception-expirer \
	&& CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags='-s -w' -o /out/rule-rollout-monitor ./cmd/rule-rollout-monitor

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=build /out/control-api /app/control-api
COPY --from=build /out/agent-credential-broker /app/agent-credential-broker
COPY --from=build /out/migrate /app/migrate
COPY --from=build /out/outbox-relay /app/outbox-relay
COPY --from=build /out/interaction-responder /app/interaction-responder
COPY --from=build /out/interaction-admitter /app/interaction-admitter
COPY --from=build /out/agent-task-source-admitter /app/agent-task-source-admitter
COPY --from=build /out/acknowledger /app/acknowledger
COPY --from=build /out/terminal-reporter /app/terminal-reporter
COPY --from=build /out/issue-publisher /app/issue-publisher
COPY --from=build /out/issue-triager /app/issue-triager
COPY --from=build /out/notifier /app/notifier
COPY --from=build /out/sso-prober /app/sso-prober
COPY --from=build /out/provider-prober /app/provider-prober
COPY --from=build /out/provider-feedback-poller /app/provider-feedback-poller
COPY --from=build /out/model-prober /app/model-prober
COPY --from=build /out/data-governance-worker /app/data-governance-worker
COPY --from=build /out/review-scheduler /app/review-scheduler
COPY --from=build /out/rule-exception-expirer /app/rule-exception-expirer
COPY --from=build /out/rule-rollout-monitor /app/rule-rollout-monitor
COPY migrations /app/migrations
EXPOSE 8080
ENTRYPOINT ["/app/control-api"]
