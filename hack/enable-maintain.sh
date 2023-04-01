#!/bin/bash

sudo curl -Lv --unix-socket /var/run/batt.sock -XPOST http://localhost/maintain
