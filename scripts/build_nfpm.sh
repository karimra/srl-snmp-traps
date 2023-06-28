#!/bin/bash

SCRIPTPATH="$( cd -- "$(dirname "$0")" >/dev/null 2>&1 ; pwd -P )"
cd $SCRIPTPATH/..

mkdir -p builds

GOOS=linux GOARCH=amd64 go build -o builds/srl-snmp-traps
nfpm pkg --packager rpm --target builds/

cp builds/*rpm lab/rpm/