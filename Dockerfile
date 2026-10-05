# Frontend build stage
FROM node:24-alpine@sha256:ebfe2f90462722a7a4de65e91990e97fe0d401c70e0e762c5b53302f905ec1c1 AS build-frontend
WORKDIR /app/frontend

# The pnpm version comes from the packageManager field of
# frontend/package.json, the same version CI uses.
RUN corepack enable

# pnpm-workspace.yaml carries pnpm settings that a frozen install checks
# against the lockfile.
COPY frontend/package.json frontend/pnpm-lock.yaml frontend/pnpm-workspace.yaml ./
RUN pnpm install --frozen-lockfile

COPY frontend/ ./
RUN pnpm build

# Go build stage
FROM golang:1.27.1-alpine@sha256:8a5910f31396cd4d89662f56c68b3ae31d374308270a1c3bd96672ee5ed43414 AS build-go
ENV CGO_ENABLED=0
ARG BUILD_VERSION=dev

WORKDIR /app

ENV GOCACHE=/root/.cache/go-build
ENV GOMODCACHE=/root/.cache/go-mod

COPY go.mod go.sum ./
RUN --mount=type=cache,target=/root/.cache/go-mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go mod download && go mod verify

COPY . /app
COPY --from=build-frontend /app/frontend/dist /app/frontend/dist

RUN --mount=type=cache,target=/root/.cache/go-mod \
    --mount=type=cache,target=/root/.cache/go-build \
    go build -mod=readonly -ldflags="-w -s -X main.version=${BUILD_VERSION}" -o robin

# Final stage. distroless/base provides the CA certificates for the Slack,
# Google, Notion, GitHub, and Claude APIs; time zones are embedded in the
# binary through time/tzdata.
FROM gcr.io/distroless/base:nonroot@sha256:fb282f8ed3057f71dbfe3ea0f5fa7e961415dafe4761c23948a9d4628c6166fe
USER nonroot
COPY --from=build-go /app/robin /robin

ENTRYPOINT ["/robin"]
