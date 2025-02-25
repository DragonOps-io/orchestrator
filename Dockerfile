ARG WORKER_VERSION
FROM 989911971741.dkr.ecr.us-east-1.amazonaws.com/dragonops-worker:${WORKER_VERSION} as worker

FROM alpine:3.18.3
WORKDIR /app
RUN apk add --no-cache bash git age aws-cli wireguard-tools
COPY orchestrator .
COPY --from=worker /app/worker .
COPY --from=worker /app/tmpl.tgz.age .
ENTRYPOINT ["/app/orchestrator"]