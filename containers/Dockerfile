ARG RHEL_VERSION
FROM registry.access.redhat.com/${RHEL_VERSION}:latest

COPY appstream.repo /etc/yum.repos.d/centos8-appstream.repo

RUN dnf install -y --nodocs make automake --enablerepo=centos9 --allowerasing  && \
    dnf install -y --nodocs gcc git bc lksctp-tools-devel --enablerepo=*

RUN git clone https://github.com/HewlettPackard/netperf
WORKDIR netperf

RUN git reset --hard 3bc455b23f901dae377ca0a558e1e32aa56b31c4 && \
    curl -O https://gist.githubusercontent.com/jtaleric/c367e06b9e3f782c042667577aa1f705/raw/71e9ed086d4c2aef24c7fce7f3d9b594873e621c/netperf.diff  && \
    git apply netperf.diff && \
    ./autogen.sh && \
    ./configure --enable-sctp=yes --enable-demo=yes && make && make install

WORKDIR ../
RUN rm -rf netperf && \
    dnf clean all && \
    curl -O https://raw.githubusercontent.com/jtaleric/tinker/main/networking/super-netperf && \
    chmod +x super-netperf
