FROM 851725405730.dkr.ecr.us-east-1.amazonaws.com/dev-worker:latest as worker

FROM alpine:3.18.3

ENV KUBECTL_VERSION=1.33.1

WORKDIR /app
RUN apk add --no-cache curl bash git age aws-cli wireguard-tools rsync \
      && curl -LO --output kubectl "https://dl.k8s.io/release/$KUBECTL_VERSION/bin/linux/amd64/kubectl" \
      && chmod +x kubectl \
      && mv kubectl /usr/local/bin/ \
      && kubectl version --client

COPY orchestrator .
COPY --from=worker /app/worker .
COPY --from=worker /app/tmpl.tgz.age .

ENTRYPOINT ["/app/orchestrator"]