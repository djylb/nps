#!/bin/sh

if [ ! -d /conf ]; then
    mkdir -p /conf
fi

if [ -f /conf/nps.conf ]; then
    /nps service -disable_remote_update
else
    cp /nps.conf.sample /conf/nps.conf
fi
