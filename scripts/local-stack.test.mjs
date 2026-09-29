import assert from "node:assert/strict";
import { spawnSync } from "node:child_process";
import { mkdtempSync, readFileSync, rmSync, writeFileSync, chmodSync } from "node:fs";
import { tmpdir } from "node:os";
import { join } from "node:path";
import test from "node:test";

const fakeDocker = `#!/usr/bin/env node
const { appendFileSync, readFileSync, writeFileSync } = require("node:fs");
const args = process.argv.slice(2);
appendFileSync(process.env.MOCK_DOCKER_LOG, args[0] + " " + args.slice(1).map((item) => item.slice(0, 80)).join(" ") + "\\n");
if (args[0] === "image") process.exit(0);
if (args[0] === "inspect") {
  const count = Number(readFileSync(process.env.MOCK_INSPECT_COUNTER, "utf8")) + 1;
  writeFileSync(process.env.MOCK_INSPECT_COUNTER, String(count));
  process.stdout.write(process.env.MOCK_RABBITMQ_HEALTH === "starting-once" && count === 1 ? "starting" : "healthy");
  process.exit(0);
}
if (args[0] === "exec") {
  if (args.includes("df")) {
    process.stdout.write("Filesystem 1024-blocks Used Available Capacity Mounted on\\noverlay 10000000 5000000 5000000 50% /\\n");
    process.exit(0);
  }
  const sql = args.at(-1) ?? "";
  process.stdout.write(sql.includes("FROM provider_health_probes probe") ? process.env.MOCK_PENDING_PROBES : "0");
  process.exit(0);
}
if (args[0] === "compose" && args.includes("config")) {
  process.stdout.write(JSON.stringify({ services: { "compact-review": { environment: { ENVIRONMENT: "development" } } } }));
  process.exit(0);
}
if (args[0] === "compose" && args.includes("ps")) {
  if (args.includes("-q")) process.stdout.write(args.at(-1) === "rabbitmq" ? "rabbit-test" : process.env.MOCK_POSTGRES_CONTAINER);
  else process.stdout.write(process.env.MOCK_RUNNING_SERVICES);
}
`;

function run(mode, options = {}) {
  const directory = mkdtempSync(join(tmpdir(), "open-review-local-stack-test-"));
  try {
    const dockerPath = join(directory, "docker");
    const logPath = join(directory, "docker.log");
    const inspectCounterPath = join(directory, "inspect-count");
    writeFileSync(dockerPath, fakeDocker);
    writeFileSync(logPath, "");
    writeFileSync(inspectCounterPath, "0");
    chmodSync(dockerPath, 0o755);
    const result = spawnSync("bash", ["scripts/local-stack.sh", mode, ...(options.build ? ["--build"] : []), "--stop-unused"], {
      cwd: new URL("..", import.meta.url),
      encoding: "utf8",
      env: {
        ...process.env,
        PATH: `${directory}:${process.env.PATH}`,
        MOCK_DOCKER_LOG: logPath,
        MOCK_PENDING_PROBES: options.pendingProbes ?? "0",
        MOCK_POSTGRES_CONTAINER: options.postgresContainer ?? "pg-test",
        MOCK_RUNNING_SERVICES: options.runningServices ?? "postgres\nprovider-prober\n",
        MOCK_RABBITMQ_HEALTH: options.rabbitmqHealth ?? "healthy",
        MOCK_INSPECT_COUNTER: inspectCounterPath,
      },
    });
    return { ...result, dockerLog: readFileSync(logPath, "utf8") };
  } finally {
    rmSync(directory, { recursive: true, force: true });
  }
}

test("does not pause workers when PostgreSQL is stopped", () => {
  const result = run("auth", { postgresContainer: "" });
  assert.equal(result.status, 1);
  assert.match(result.stderr, /Cannot verify pending work while PostgreSQL is stopped/);
  assert.doesNotMatch(result.dockerLog, /\b(stop|up)\b/);
});

test("auth mode refuses to strand a queued provider probe", () => {
  const result = run("auth", { pendingProbes: "1" });
  assert.equal(result.status, 1);
  assert.match(result.stderr, /provider verification job/);
  assert.doesNotMatch(result.dockerLog, /\b(stop|up)\b/);
});

test("setup mode keeps the provider prober for a pending probe", () => {
  const result = run("setup", { pendingProbes: "1" });
  assert.equal(result.status, 0, result.stderr);
  assert.match(result.dockerLog, /\bup\b/);
  assert.doesNotMatch(result.dockerLog, /stop provider-prober/);
});

test("compact rebuild waits for RabbitMQ health before starting consumers", () => {
  const result = run("compact", {
    build: true,
    rabbitmqHealth: "starting-once",
    runningServices: "postgres\nrabbitmq\ncontrol-api\nconsole\ncompact-review\n",
  });
  assert.equal(result.status, 0, result.stderr);
  const inspect = result.dockerLog.indexOf("inspect --format");
  const recreate = result.dockerLog.indexOf("--force-recreate --no-build --pull never compact-review");
  assert.ok(inspect >= 0, result.dockerLog);
  assert.equal((result.dockerLog.match(/inspect --format/g) ?? []).length, 2, result.dockerLog);
  assert.ok(recreate > inspect, result.dockerLog);
});
