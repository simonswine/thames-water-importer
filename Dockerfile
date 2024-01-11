FROM golang:1.21.6

WORKDIR /workspace

# install dependencies and download them
COPY go.mod go.sum ./
RUN go mod download

# copy files
COPY ./main.go ./
COPY ./api ./api
COPY ./app ./app

RUN CGO_ENABLED=0 GOOS=linux go build -o thames-water-importer ./

FROM browserless/chrome:1.60.1-chrome-stable

WORKDIR /tmp

COPY --from=0 /workspace/thames-water-importer /usr/bin

USER blessuser

ENTRYPOINT ["thames-water-importer","--chrome-sandbox=false"]
