#!/bin/sh
# Run only inside the dedicated delegated user scope documented below.
set -eu
relative_group=$(awk -F: '$1 == "0" {print $3}' /proc/self/cgroup)
case "$relative_group" in
  */opencompute-contributor.scope) ;;
  *) echo 'Use systemd-run --user --scope --unit=opencompute-contributor --property=Delegate=yes with this script.' >&2; exit 1 ;;
esac
group=/sys/fs/cgroup$relative_group
mkdir -p "$group/manager"
echo $$ > "$group/manager/cgroup.procs"
printf '+cpu +memory +pids' > "$group/cgroup.subtree_control"
mkdir -p "$group/workloads"
printf '+cpu +memory +pids' > "$group/workloads/cgroup.subtree_control"
export OPENCOMPUTE_CGROUP_ROOT="$group/workloads"
exec "$@"
