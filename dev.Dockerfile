FROM golang:1.21-alpine as build

ARG GITHUB_API_REPO_PATH
ENV GOPRIVATE="github.com/DragonOps-io/*"
RUN apk add --no-cache file git rsync openssh-client
RUN mkdir -p -m 0700 ~/.ssh && ssh-keyscan github.com >> ~/.ssh/known_hosts

WORKDIR /app

RUN --mount=type=ssh <<EOT
  set -e
  echo "Setting Git SSH protocol"
  git config --global url."git@github.com:".insteadOf "https://github.com/"
  (
    set +e
    ssh -T git@github.com
    if [ ! "$?" = "1" ]; then
      echo "No GitHub SSH key loaded exiting..."
      exit 1
    fi
  )
EOT

COPY . .

RUN  --mount=type=ssh \
      CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o .

FROM 124269415015.dkr.ecr.us-east-1.amazonaws.com/dev-worker:latest as worker

FROM alpine:3.18.3

WORKDIR /app
RUN apk add --no-cache bash git age
COPY --from=build /app/orchestrator .
COPY --from=worker /app/worker .
COPY --from=worker /app/tmpl.tgz.age .

ENTRYPOINT ["/app/orchestrator"]