# Features

One entry per feature file. A feature has an entry only when its Drive runs.

- [embedded-page](embedded-page.md): the empty web page embedded in the binary, served at `/`.
- [local-login](local-login.md): one-time login address, per-tab session and stop screen on the 127.0.0.1 server.
- [cluster-connect](cluster-connect.md): connection to the current kubeconfig context, context switch, retry and the failure states.
- [graph-containment](graph-containment.md): which object each Pod, ReplicaSet, Deployment, StatefulSet, DaemonSet, Job and CronJob sits inside, from ownerReferences, and why an owner is not used.
- [graph-bridges](graph-bridges.md): the declared references between objects of a namespace (Ingress, Service, Pod, PVC, PV, ConfigMap, Secret, HPA, HTTPRoute) and the settings that made each one.
- [graph-states](graph-states.md): whether each bridge holds (connected, waiting, broken, warning, unknown, optional) with the text of what it looks for, the cluster answer with all namespaces and the cluster band, and the namespace states empty, not found and no permission.
- [appendix-a](appendix-a.md): the six Appendix A faults and the healthy baseline built on a kind cluster, judged from the graph API by a script that prints each scenario, A-0 and the total; Pod nodes show their state and text.
