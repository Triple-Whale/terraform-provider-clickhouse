FROM golang:1.26.10-bookworm

ARG TARGETARCH
ARG TERRAFORM_VERSION=1.10.5
RUN apt-get update && apt-get install -y --no-install-recommends unzip \
    && cd /tmp \
    && curl -fsSLO "https://releases.hashicorp.com/terraform/${TERRAFORM_VERSION}/terraform_${TERRAFORM_VERSION}_linux_${TARGETARCH}.zip" \
    && curl -fsSLO "https://releases.hashicorp.com/terraform/${TERRAFORM_VERSION}/terraform_${TERRAFORM_VERSION}_SHA256SUMS" \
    && grep " terraform_${TERRAFORM_VERSION}_linux_${TARGETARCH}.zip$" "terraform_${TERRAFORM_VERSION}_SHA256SUMS" | sha256sum --check --status \
    && unzip "terraform_${TERRAFORM_VERSION}_linux_${TARGETARCH}.zip" terraform -d /usr/local/bin

WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
ENV TF_IN_AUTOMATION=1
