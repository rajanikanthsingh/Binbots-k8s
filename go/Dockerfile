FROM golang:1.21-alpine AS builder
WORKDIR /app
COPY go.mod ./
RUN go mod download
COPY . .
RUN CGO_ENABLED=0 go build -o /k8s-ai-exporter .

FROM alpine:3.19
RUN apk --no-cache add ca-certificates
COPY --from=builder /k8s-ai-exporter /k8s-ai-exporter
EXPOSE 9100
ENTRYPOINT ["/k8s-ai-exporter"]
