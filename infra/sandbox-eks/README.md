# Dedicated EKS sandbox

This configuration creates one **new** EKS cluster named `knull-sandbox` in
Mumbai (`ap-south-1`). It has one managed `m5.large` worker in a private
subnet, one NAT gateway, no SSH access, and VPC CNI network policies enabled
in strict mode. It is for temporary validation runs only. Do not point it at
any production kubeconfig or copy production Secrets or data into it.

## Before creation

An AWS account with billing enabled and an AWS identity allowed to create
EKS, IAM, VPC, EC2, and CloudFormation resources are required. Use a named
AWS CLI profile and verify the account and region before creating anything:

```sh
aws sts get-caller-identity
aws configure get region
eksctl create cluster -f infra/sandbox-eks/cluster.yaml --dry-run
```

Creating the cluster starts charges. AWS lists the standard EKS control plane
at **$0.10/hour**, plus the managed EC2 worker, its EBS volume, NAT gateway,
public IPv4, and traffic. A continuously running control plane alone costs
about **$73 for 730 hours**; total cost is higher. Review the
[EKS pricing](https://aws.amazon.com/eks/pricing/) and
[VPC pricing](https://aws.amazon.com/vpc/pricing/) before creation. Delete the
cluster after the live demo.

## Creation and teardown

Run only after confirming the AWS account and cost:

```sh
eksctl create cluster -f infra/sandbox-eks/cluster.yaml
kubectl --context <new-sandbox-context> get nodes
eksctl delete cluster -f infra/sandbox-eks/cluster.yaml
```

Before enabling Knull's sandbox runner, verify the VPC CNI add-on is active
with network policy enabled and strict enforcement, run a network-denial
probe, and set up a sandbox-only Kubernetes identity with narrowly scoped
RBAC. The cluster creator's admin kubeconfig must **not** be used by the
runner. The runner requires its own kubeconfig with only the sandbox context,
plus the sandbox cluster's `kube-system` namespace UID and an explicit list
of production cluster UIDs. See `backend/.env.example`.

The `checkout-fixture` image must be pushed to a pull-only registry and
referenced by digest. TrueForge's code sandbox and sandbox-local Prometheus
must be configured separately; creating EKS alone does not complete Task 18.
