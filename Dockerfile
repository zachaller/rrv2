FROM golang:1.23 AS build
WORKDIR /src
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -trimpath -ldflags "-s -w" -o /out/rollouts-controller ./cmd/rollouts-controller

FROM gcr.io/distroless/static:nonroot
COPY --from=build /out/rollouts-controller /usr/local/bin/rollouts-controller
USER 65532:65532
ENTRYPOINT ["/usr/local/bin/rollouts-controller"]
