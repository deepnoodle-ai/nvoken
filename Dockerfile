FROM golang:1.26.2-bookworm AS build

ARG TARGETOS=linux
ARG TARGETARCH=amd64
ARG NVOKEN_BUILD_VERSION=devel

WORKDIR /src

COPY go.mod go.sum ./
RUN go mod download

COPY . .
RUN CGO_ENABLED=0 GOOS=$TARGETOS GOARCH=$TARGETARCH \
    go build -trimpath -ldflags="-s -w -X main.buildVersion=${NVOKEN_BUILD_VERSION}" -o /out/nvokend ./cmd/nvokend

FROM gcr.io/distroless/static-debian12:nonroot

COPY --from=build /out/nvokend /nvokend

EXPOSE 8080

ENTRYPOINT ["/nvokend"]
CMD ["serve"]
