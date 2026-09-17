FROM golang:1.25-bookworm AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY cmd ./cmd
COPY internal ./internal
RUN CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags='-s -w' -o /out/control-api ./cmd/control-api \
    && CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags='-s -w' -o /out/migrate ./cmd/migrate \
    && CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags='-s -w' -o /out/outbox-relay ./cmd/outbox-relay \
    && CGO_ENABLED=0 GOOS=linux go build -trimpath -ldflags='-s -w' -o /out/interaction-responder ./cmd/interaction-responder

FROM gcr.io/distroless/static-debian12:nonroot
WORKDIR /app
COPY --from=build /out/control-api /app/control-api
COPY --from=build /out/migrate /app/migrate
COPY --from=build /out/outbox-relay /app/outbox-relay
COPY --from=build /out/interaction-responder /app/interaction-responder
COPY migrations /app/migrations
EXPOSE 8080
ENTRYPOINT ["/app/control-api"]
