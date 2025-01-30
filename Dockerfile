FROM golang:1.23-alpine

WORKDIR /app
COPY . .
RUN go build -o /bin/scaler

FROM alpine:3.18
RUN apk add --no-cache tzdata
COPY --from=0 /bin/scaler /bin/scaler
ENTRYPOINT ["/bin/scaler"] 