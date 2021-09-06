FROM ubuntu:20.04

ENV DEBIAN_FRONTEND=noninteractive

# RUN apt update
RUN apt-get update
RUN apt-get install -y vim golang ca-certificates

VOLUME ["/app"]

WORKDIR /app

EXPOSE 9858

CMD ["go", "run", "sysload_exporter.go"]
