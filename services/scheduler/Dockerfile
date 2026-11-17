FROM golang:1.25.4-alpine AS build
WORKDIR /src
COPY services/scheduler/ ./
RUN CGO_ENABLED=0 go build -trimpath -ldflags='-s -w' -o /scheduler .
FROM scratch
COPY --from=build /etc/ssl/certs/ca-certificates.crt /etc/ssl/certs/ca-certificates.crt
COPY --from=build /scheduler /scheduler
USER 65532:65532
ENTRYPOINT ["/scheduler"]
