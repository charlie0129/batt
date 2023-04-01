#!/bin/bash

# must have one argument
if [ $# -ne 1 ]; then
    echo "Usage: $0 <limit>"
    exit 1
fi

sudo curl -Lv --unix-socket /var/run/batt.sock -XPUT http://localhost/limit --data "$1"
