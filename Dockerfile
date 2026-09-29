FROM golang:alpine AS builder

WORKDIR /app

RUN apk add --no-cache git ca-certificates tzdata

COPY go.mod go.sum ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -ldflags="-s -w -X github.com/adnannpm/Bloomsom/internal/version.Engine=0.1.0" -o bloomsom main.go

FROM alpine:3.20

RUN apk add --no-cache ca-certificates tzdata

WORKDIR /app

COPY --from=builder /app/bloomsom /usr/local/bin/bloomsom

EXPOSE 7777 7778/udp

VOLUME ["/app/data"]

ENV BLOOMSOM_DATABASE_PATH=/app/data/bloomsom.db
ENV BLOOMSOM_SERVER_HOST=0.0.0.0

ENTRYPOINT ["bloomsom"]
CMD ["start"]
