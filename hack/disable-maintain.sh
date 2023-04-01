#!/bin/bash

sudo curl -Lv --unix-socket /var/run/batt.sock -XDELETE http://localhost/maintain