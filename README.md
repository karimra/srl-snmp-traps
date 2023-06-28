# SRL-SNMP-TRAPS

### configuration

CLI:

```bash
enter candidate
system gnmi-server unix-socket admin-state enable
system gnmi-server unix-socket use-authentication false

system snmp-traps destination 10.0.0.1:161 admin-state enable
system snmp-traps destination 10.0.0.1:161 community private
system snmp-traps destination 10.0.0.1:161 network-instance mgmt
commit now
```

gNMI:

```bash
dest_addr=10.0.0.1:161
gnmic -a clab-traps-srl1 -u admin -p NokiaSrl1! --skip-verify \
    -e json_ietf \
    set \
    --update-path /system/gnmi-server/unix-socket/admin-state \
    --update-value enable \
    --update-path /system/gnmi-server/unix-socket/use-authentication \
    --update-value false \
    --update-path /system/snmp-traps/destination[address=${dest_addr}]/admin-state \
    --update-value enable \
    --update-path /system/snmp-traps/destination[address=${dest_addr}]/community \
    --update-value private \
    --update-path /system/snmp-traps/destination[address=${dest_addr}]/network-instance \
    --update-value mgmt
```

### traps definition

Trap definitions are YAML files located under `/opt/snmp-traps/traps`

example:

```yaml
# trap definition name
# used mainly for logging
name: interface_oper_state

# trigger defines which gNMI path triggers
# the trap generation.
trigger:
  # keyless gNMI path
  path: /interface/oper-state
  
  # condition is an optional attribute.
  # it's a jq expression that must return a boolean result.
  # it allows to filter in/out specific traps.
  # if the condition is true the trap is triggered
  # condition: '.tags.interface_name != mgmt0'
  
  # publish defines a list of variables to be
  # built from the message that triggered the trap
  # and published to be used in 'tasks' and/or
  # in the trap PDU section
  publish:
    - if_name: .tags.interface_name
    - oper_state: |
        if (.values."/interface/oper-state" == "up") 
        then 1 
        else 2 
        end

# `tasks` defines a list of tasks to run sequentially.
# The goal is to retrieve extra variables from the SRL gNMI server
# to enrich the trap with extra attributes.
# Each task can publish one or more attributes.
tasks:
  - name: get_if_index
    gnmi:
      rpc: get
      path: '"/interface[name=" + $if_name + "]/ifindex"'
      encoding: ascii
    publish:
      - ifindex: '.values."/interface/ifindex"'

  - name: get_hostname
    gnmi:
      rpc: get
      path: '"/system/name/host-name"'
      encoding: ascii
    publish:
      - hostname: '.values."/system/name/host-name"'

  - name: get_admin_state
    gnmi:
      rpc: get
      path: '"/interface[name="+ $if_name +"]/admin-state"'
      encoding: ascii
    publish:
      - admin_state: |
          if (.values."/interface/admin-state" == "enable") 
          then 1 
          else 2 
          end

# trap describes the actual trap being generated
trap:
  # inform specifies if the generated trap is an inform PDU
  inform: false
  # community allows to customize the community string
  # in the trap PDU.
  # if empty the community configured on the node under 
  # /system/snmp-traps/destination[name=*]/community is used.
  community: '"dummy"'
  # bindings define the list of variables to be added 
  # to the trap.
  # the app will add the OID 1.3.6.1.2.1.1.3.0 (sysUptime)
  # as first OID. It will has as value the uptime of the app
  # not SRL's.
  bindings:
    - oid: '".1.3.6.1.2.1.2.2.1.8."+ $ifindex'
      type: int
      value: $oper_state
    - oid: '".1.3.6.1.2.1.1.5"'
      type: octetString
      value: $hostname
    - oid: '".1.3.6.1.2.1.2.2.1.7."+ $ifindex'
      type: int
      value: $admin_state
```
