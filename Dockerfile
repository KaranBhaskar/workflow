FROM golang:1.26.0 AS build

WORKDIR /src

COPY go.mod ./
RUN go mod download

COPY . .

RUN CGO_ENABLED=0 GOOS=linux go build -o /out/api ./cmd/api
RUN CGO_ENABLED=0 GOOS=linux go build -o /out/worker ./cmd/worker

FROM gcr.io/distroless/static-debian12:nonroot AS api

COPY --from=build /out/api /app

ENTRYPOINT ["/app"]

FROM gcr.io/distroless/static-debian12:nonroot AS worker

COPY --from=build /out/worker /app

ENTRYPOINT ["/app"]
