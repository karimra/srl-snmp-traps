#!/bin/bash

SCRIPTPATH="$( cd -- "$(dirname "$0")" >/dev/null 2>&1 ; pwd -P )"
cd $SCRIPTPATH/..

lab_name=traps
username=admin
password=NokiaSrl1!

# build comma separated srl nodes names
srl_nodes=$(docker ps -f label=clab-node-kind=srl -f label=containerlab=$lab_name --format {{.Names}} | paste -s -d, -)

gnmic_cmd="gnmic -u $username -p $password --skip-verify"

${gnmic_cmd} -a ${srl_nodes} set --request-file lab/config.yaml

${gnmic_cmd} -a ${srl_nodes} get -e json_ietf --path /system/snmp-traps
