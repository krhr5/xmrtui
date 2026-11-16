#!/bin/bash
# Launcher wrapper: keeps the window open if xmrtui exits with an error.
"$(dirname "$(readlink -f "$0")")/xmrtui" "$@" || read -n 1 -s -p "Press any key to close"
