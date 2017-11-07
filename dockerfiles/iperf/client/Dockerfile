FROM alpine:3.6

EXPOSE 5001

RUN apk update && apk add iperf
ENV SERVER_MGMT ''
ENV TIME '150'
#CMD iperf -c $SERVER_MGMT -t 150
ENTRYPOINT echo "Connecting to $SERVER_MGMT" && iperf -c "$SERVER_MGMT"
