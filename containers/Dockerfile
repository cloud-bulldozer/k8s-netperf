FROM registry.access.redhat.com/ubi8:8.6

COPY appstream.repo /etc/yum.repos.d/centos8-appstream.repo
RUN dnf install -y --nodocs make automake --enablerepo=centos9 --allowerasing
RUN dnf install -y --nodocs gcc git --enablerepo=centos9 --allowerasing
RUN git clone https://github.com/HewlettPackard/netperf
WORKDIR netperf
RUN git reset --hard 3bc455b23f901dae377ca0a558e1e32aa56b31c4 
RUN ./autogen.sh
RUN ./configure --enable-demo && make && make install
WORKDIR ../
RUN rm -rf netperf/
RUN dnf remove gcc make automake git -y
