FROM alpine:3.6

EXPOSE 5001

RUN apk update && apk add iperf

ENTRYPOINT ["iperf", "-s"]
