FROM golang:1.22-alpine AS build
WORKDIR /app
COPY server/ ./
RUN go build -o server .

FROM alpine:3.20
COPY --from=build /app/server /usr/local/bin/server
CMD ["server"]
