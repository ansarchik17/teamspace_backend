FROM ubuntu:24.04
COPY install-runtime.sh /tmp/install-runtime.sh
RUN sed -i 's/\r$//' /tmp/install-runtime.sh && sh /tmp/install-runtime.sh && rm /tmp/install-runtime.sh
ENV HOME=/home/app
WORKDIR /app
