# syntax=docker/dockerfile:1.7
# Multi-arch minimal runtime image for RuleRefinery
FROM gcr.io/distroless/static:nonroot

WORKDIR /app

ARG TARGETPLATFORM
# Binary will be injected by GoReleaser per-arch build context (dockers_v2)
COPY $TARGETPLATFORM/rulerefinery /app/rulerefinery
# Optional: include default config for quick start
COPY config.yaml /app/config.yaml

USER nonroot:nonroot
ENTRYPOINT ["/app/rulerefinery"]
