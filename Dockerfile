FROM golang:1.23.5

WORKDIR /workspace

# install dependencies and download them
COPY go.mod go.sum ./
RUN go mod download

# copy files
COPY ./main.go ./
COPY ./api ./api
COPY ./app ./app

RUN CGO_ENABLED=0 GOOS=linux go build -o thames-water-importer ./

FROM ghcr.io/browserless/chromium:v2.24.3

WORKDIR /tmp

COPY --from=0 /workspace/thames-water-importer /usr/bin

USER blessuser

ENTRYPOINT ["thames-water-importer","--chrome-sandbox=false"]
