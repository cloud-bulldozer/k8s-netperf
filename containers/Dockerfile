FROM registry.access.redhat.com/ubi9:latest

COPY appstream.repo /etc/yum.repos.d/centos8-appstream.repo
RUN dnf install -y --nodocs make automake --enablerepo=centos9 --allowerasing
RUN dnf install -y --nodocs gcc git bc lksctp-tools-devel --enablerepo=*
RUN git clone https://github.com/HewlettPackard/netperf
WORKDIR netperf
RUN git reset --hard 3bc455b23f901dae377ca0a558e1e32aa56b31c4 
RUN curl -O https://gist.githubusercontent.com/jtaleric/c367e06b9e3f782c042667577aa1f705/raw/71e9ed086d4c2aef24c7fce7f3d9b594873e621c/netperf.diff
RUN git apply netperf.diff
RUN ./autogen.sh
RUN ./configure --enable-sctp=yes --enable-demo=yes && make && make install
WORKDIR ../
RUN rm -rf netperf/
RUN curl -O https://raw.githubusercontent.com/jtaleric/tinker/main/networking/super-netperf
RUN chmod +x super-netperf
