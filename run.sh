#!/bin/bash

cd "$CANAL_DIR/bin"

if [[ "$(grep -c '^JAVA_OPTS=' startup.sh)" -ne 1 ]]; then
  echo 'startup.sh JAVA_OPTS incorrect'
  exit 1
fi

if [[ "$(grep -c '^CANAL_OPTS=' startup.sh)" -ne 1 ]]; then
  echo 'startup.sh CANAL_OPTS incorrect'
  exit 1
fi

#if [[ -n "$K_DESTINATION" && "$K_DESTINATION" != example ]]; then
#  if [[ "$K_DESTINATION" =~ ',' ]]; then
#    echo 'multi canal.destinations unsupported'
#    exit 1
#  fi
#  mv ../conf/example "../conf/$K_DESTINATION"
#fi

if [[ -n "$K_JAVA_OPTS" ]]; then
  sed -i '/^JAVA_OPTS=/a \
JAVA_OPTS=" $JAVA_OPTS $K_JAVA_OPTS "
' startup.sh
fi

sed -i '/^CANAL_OPTS=/a \
CANAL_OPTS=" $CANAL_OPTS $K_CANAL_OPTS "
' startup.sh

if [[ "$K_LOCAL" == 'true' ]]; then
  bash restart.sh local
else
  bash restart.sh
fi

cm=/configmaps
while true; do

while read i; do

if [[ -z "$i" ]]; then
  continue
fi

w=true
f="../conf/$i/instance.properties"
if [[ -f "$f" ]]; then
  if [[ "$(cat "$f" | md5sum)" == "$(cat "$cm/$i" | md5sum)" ]]; then
    w=false
  fi
else
  if [[ ! -d "../conf/$i" ]]; then
    echo "[create] $i"
    mkdir "../conf/$i"
  fi
fi
if [[ "$w" == true ]]; then
  echo "[reload] $i"
  cat "$cm/$i" > "$f"
fi

done <<< "$(ls "$cm")"

while read i; do

if [[ -z "$i" ]]; then
  continue
fi

d="$(dirname "$i")"
if [[ "$(dirname "$d")" == ../conf ]]; then
  i="$(basename "$d")"
  if [[ ! -e "$cm/$i" ]]; then
    echo "[remove] $i"
    rm -rf "$d"
  fi
fi

done <<< "$(find ../conf -type f -name instance.properties)"

sleep 10
done
