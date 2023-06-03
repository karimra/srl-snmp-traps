#!/bin/bash

SCRIPTPATH="$( cd -- "$(dirname "$0")" >/dev/null 2>&1 ; pwd -P )"
cd $SCRIPTPATH/..

lab_name=traps
username=admin
password=NokiaSrl1!

# build comma separated srl nodes names
srl_nodes=$(docker ps -f label=clab-node-kind=srl -f label=containerlab=$lab_name --format {{.Names}} | paste -s -d, -)

gnmic_cmd="gnmic -u $username -p $password --skip-verify"

${gnmic_cmd} -a ${srl_nodes} set --request-file lab/prereq_config.yaml


clab_exec_labels="--label containerlab=$lab_name --label clab-node-kind=srl"
# install the RPM located in /tmp/rpm
sudo clab exec --topo lab/$lab_name.clab.yaml $clab_exec_labels --cmd "sudo rpm -U /tmp/rpm/*rpm"
sleep 1

# reload the app manager so it picks up the newly installed app
sudo clab exec --topo lab/$lab_name.clab.yaml $clab_exec_labels --cmd "sr_cli tools system app-management application app_mgr reload"
sleep 1

# check the app status in SRL(s)
sudo clab exec --topo lab/$lab_name.clab.yaml $clab_exec_labels --cmd "sr_cli show system application snmp-traps"