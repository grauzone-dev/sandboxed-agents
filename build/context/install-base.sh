#!/bin/sh
set -eu

export DEBIAN_FRONTEND=noninteractive
printf '#!/bin/sh\nexit 101\n' > /usr/sbin/policy-rc.d
chmod 0755 /usr/sbin/policy-rc.d

apt-get update
apt-get install -y --no-install-recommends \
    bash ca-certificates coreutils curl findutils gh git gnupg jq less \
    openssh-client openssh-server procps ripgrep tar tini tmux unzip util-linux xz-utils

mkdir -p /etc/apt/keyrings
curl -fsSL https://deb.nodesource.com/gpgkey/nodesource-repo.gpg.key -o /tmp/nodesource.asc
gpg --batch --dearmor -o /etc/apt/keyrings/nodesource.gpg /tmp/nodesource.asc
chmod 0644 /etc/apt/keyrings/nodesource.gpg
printf 'deb [arch=amd64 signed-by=/etc/apt/keyrings/nodesource.gpg] https://deb.nodesource.com/node_24.x nodistro main\n' \
    > /etc/apt/sources.list.d/nodesource.list
apt-get update
apt-get install -y --no-install-recommends nodejs

groupadd --gid 1000 agent
useradd --uid 1000 --gid 1000 --create-home --shell /bin/bash agent
mkdir -p /workspace
chown agent:agent /workspace

rm -f /etc/ssh/ssh_host_* /usr/sbin/policy-rc.d /tmp/nodesource.asc
rm -rf /var/lib/apt/lists/* /root/.gnupg
