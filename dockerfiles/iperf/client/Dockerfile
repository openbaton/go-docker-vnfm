FROM ubuntu:16.04

EXPOSE 5001

RUN apt update && apt install iperf

CMD iperf -c $SERVER_PRIVATE_FLOATINGIP -t 150
