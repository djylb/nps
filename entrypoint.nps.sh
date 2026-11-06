#!/bin/sh

if [ ! -d /conf ]; then
    mkdir -p /conf
fi

# Geodata of the image replaces missing or older files; newer files from
# nps update-geo are kept.
image_version="$(cat /geodata.version.sample 2>/dev/null)"
conf_version="$(cat /conf/geodata.version 2>/dev/null)"
for name in geoip.dat geosite.dat; do
    if [ -f "/$name.sample" ] && { [ ! -f "/conf/$name" ] || [ "$image_version" \> "$conf_version" ]; }; then
        cp "/$name.sample" "/conf/$name.download" && mv "/conf/$name.download" "/conf/$name"
    fi
done
if [ -n "$image_version" ] && [ "$image_version" \> "$conf_version" ]; then
    echo "$image_version" > /conf/geodata.version
fi

if [ -f /conf/nps.conf ]; then
    /nps service -disable_remote_update
else
    cp /nps.conf.sample /conf/nps.conf
fi
