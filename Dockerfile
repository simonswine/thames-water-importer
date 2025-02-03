FROM docker.io/library/golang:1.23.5

WORKDIR /workspace

# install dependencies and download them
COPY go.mod go.sum ./
RUN go mod download

# copy files
COPY ./main.go ./
COPY ./api ./api
COPY ./app ./app

RUN CGO_ENABLED=0 GOOS=linux go build -o thames-water-importer ./

FROM docker.io/library/debian:bookworm

WORKDIR /tmp

COPY --from=0 /workspace/thames-water-importer /usr/bin

RUN apt-get update && apt-get install -y chromium

USER nobody

ENV XDG_CONFIG_HOME=/tmp/.chromium
ENV XDG_CACHE_HOME=/tmp/.chromium

ENTRYPOINT ["thames-water-importer","--chrome-sandbox=false"]
