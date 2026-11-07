#!/bin/sh

if [ ! -d /conf ]; then
    mkdir -p /conf
fi

# Geodata of the image replaces missing or older files; newer files from
# nps update-geo are kept.
for name in geoip.dat geosite.dat; do
    image_version="$(cat "/$name.version.sample" 2>/dev/null)"
    conf_version="$(cat "/conf/$name.version" 2>/dev/null)"
    if [ -f "/$name.sample" ] && { [ ! -f "/conf/$name" ] || [ "$image_version" \> "$conf_version" ]; }; then
        cp "/$name.sample" "/conf/$name.download" && mv "/conf/$name.download" "/conf/$name"
        echo "$image_version" > "/conf/$name.version"
    fi
done

if [ -f /conf/nps.conf ]; then
    /nps service -disable_remote_update
else
    cp /nps.conf.sample /conf/nps.conf
fi
