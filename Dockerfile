ARG WORKER_VERSION
FROM 989911971741.dkr.ecr.us-east-1.amazonaws.com/dragonops-worker:${WORKER_VERSION} as worker

FROM alpine:3.18.3
WORKDIR /app
RUN apk add --no-cache curl bash git age aws-cli wireguard-tools rsync \
      &&  curl -L --output kubectl "https://dl.k8s.io/release/$(curl -L -s https://dl.k8s.io/release/stable.txt)/bin/linux/amd64/kubectl" \
      && chmod +x kubectl \
      && mv kubectl /usr/local/bin/ \
      && kubectl version --client

COPY orchestrator .
COPY --from=worker /app/worker .
COPY --from=worker /app/tmpl.tgz.age .
ENTRYPOINT ["/app/orchestrator"]