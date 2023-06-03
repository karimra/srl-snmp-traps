#!/bin/bash

SCRIPTPATH="$( cd -- "$(dirname "$0")" >/dev/null 2>&1 ; pwd -P )"
cd $SCRIPTPATH

./build.sh

cd ..
lab_name=traps
sudo clab deploy -t lab/traps.clab.yaml -c


./scripts/install.sh

sleep 5

./scripts/configure.sh
