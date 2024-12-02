FROM docker.io/library/alpine

RUN \
    ARCH=$(if [ $(uname -m) == "x86_64" ] && [ $(getconf LONG_BIT) == "64" ]; then echo "amd64"; \
         elif [ $(uname -m) == "x86_64" ] && [ $(getconf LONG_BIT) == "32" ]; then echo "386"; \
         elif [ $(uname -m) == "aarch64" ]; then echo "arm64"; \
         elif [ $(uname -m) == "armv7l" ]; then echo "arm"; \
         elif [ $(uname -m) == "armv6l" ]; then echo "arm"; fi;) && \
    echo "Building for $ARCH" 1>&2 && \
    apk add --no-cache --virtual build-dependencies --update wget curl ca-certificates && \
    apk add --no-cache wine && \
    wget -P /tmp https://github.com/$(curl -s -L https://github.com/kmlebedev/txmlconnector/releases/latest | egrep -o "kmlebedev/txmlconnector/releases/download/.*/windows_$ARCH.tar.gz") && \
    tar -C /usr/bin/ -xzvf /tmp/windows_$ARCH.tar.gz && \
    apk del build-dependencies && \
    rm -fr /var/cache/apk/* && \
    rm -rf /tmp/* && \
    mkdir -p logs

COPY txmlconnector64-6.43.2.24.0.dll /usr/bin/

ENTRYPOINT ["wine64 /usr/bin/txmlconnector-server.exe"]