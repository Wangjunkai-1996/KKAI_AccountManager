#!/bin/bash
set -euo pipefail

# The login helper stays private on sys1. SSH carries the page and API requests.
echo "打开 http://127.0.0.1:18082；保持此终端开启，Ctrl+C 关闭连接。"
exec ssh -N -T -o ExitOnForwardFailure=yes -o ServerAliveInterval=30 -o ServerAliveCountMax=3 -L 127.0.0.1:18082:127.0.0.1:18082 sys1
