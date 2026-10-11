# libmbus 0.10.3 (BSD-3-Clause, https://github.com/rscada/libmbus), built
# from its tag and checked against the commit the tests are pinned to.
# Only used by libmbus_test.go; nothing of libmbus is kept here.
FROM debian:bookworm-20240926-slim
RUN apt-get update && apt-get install -y --no-install-recommends \
      ca-certificates git gcc make autoconf automake libtool libltdl-dev libc6-dev \
    && rm -rf /var/lib/apt/lists/*
RUN git clone --depth 1 --branch 0.10.3 https://github.com/rscada/libmbus /src \
    && test "$(git -C /src rev-parse HEAD)" = d617bd2bf654fc1b0d291a340672727440853142
WORKDIR /src
# The TCP answer timeout is lowered from 4 s to 1 s so that a primary
# scan of the 251 addresses ends in about four minutes; nothing else is
# changed.
RUN sed -i "s/static int tcp_timeout_sec = 4;/static int tcp_timeout_sec = 1;/" mbus/mbus-tcp.c \
    && grep -q "tcp_timeout_sec = 1;" mbus/mbus-tcp.c \
    && ./build.sh && make install && ldconfig
