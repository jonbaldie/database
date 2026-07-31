FROM golang:1.24 AS build
WORKDIR /src
COPY go.mod ./
COPY . .
RUN CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags='-s -w' -o /out/database ./cmd/database

FROM scratch
COPY --from=build /out/database /database
ENTRYPOINT ["/database"]
