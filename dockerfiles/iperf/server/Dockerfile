FROM ubuntu:16.04

EXPOSE 5001

RUN apt update && apt install iperf

ENTRYPOINT ["iperf", "-s"]
