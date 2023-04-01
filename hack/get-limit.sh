#!/bin/bash

sudo curl -Lv --unix-socket /var/run/batt.sock http://localhost/limit
