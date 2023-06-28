#!/bin/bash

SCRIPTPATH="$( cd -- "$(dirname "$0")" >/dev/null 2>&1 ; pwd -P )"
cd $SCRIPTPATH

cd ..
lab_name=traps
sudo clab des -t lab/$lab_name.clab.yaml -c
