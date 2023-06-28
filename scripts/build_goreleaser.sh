#!/bin/bash

SCRIPTPATH="$( cd -- "$(dirname "$0")" >/dev/null 2>&1 ; pwd -P )"
cd $SCRIPTPATH/..

goreleaser release --snapshot --clean
rm -rf lab/rpm/*
cp dist/srl-snmp-traps_v*_Linux_x86_64.rpm lab/rpm/