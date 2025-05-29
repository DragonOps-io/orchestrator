FROM 851725405730.dkr.ecr.us-east-1.amazonaws.com/dev-worker:latest as worker

FROM alpine:3.18.3

WORKDIR /app
RUN apk add --no-cache bash git age aws-cli wireguard-tools rsync
COPY orchestrator .
COPY --from=worker /app/worker .
COPY --from=worker /app/tmpl.tgz.age .

ENTRYPOINT ["/app/orchestrator"]