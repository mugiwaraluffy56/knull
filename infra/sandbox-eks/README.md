# Dedicated EKS sandbox

This configuration creates one **new** EKS cluster named `knull-sandbox` in
Mumbai (`ap-south-1`). It has one managed `m5.large` worker in a private
subnet, one NAT gateway, no SSH access, and VPC CNI network policies enabled
in strict mode. It is for temporary validation runs only. Do not point it at
any production kubeconfig or copy production Secrets or data into it.

## Before creation

An AWS account with billing enabled and an identity allowed to create EKS, IAM,
VPC, EC2, and CloudFormation resources are required. Prefer a non-root IAM or
SSO identity for administration. Use a named AWS CLI profile and verify the
account and region before creating anything.
Set `AWS_PROFILE` to that profile in the shell used for every `eksctl` command:

```sh
export AWS_PROFILE=<sandbox-admin-profile>
aws sts get-caller-identity
aws eks list-clusters --region ap-south-1
eksctl create cluster -f infra/sandbox-eks/cluster.yaml --dry-run
```

Confirm the reported ARN is the intended identity in the intended account. The
cluster file fixes the region to `ap-south-1`; a CLI default region does not
override it. If `eksctl` cannot use the profile's temporary credentials, stop
and fix its credential provider before creating resources. The account root
profile should be used only for an explicitly authorized bootstrap session;
never put those credentials in the application or runner kubeconfig.

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
export AWS_PROFILE=<sandbox-admin-profile>
eksctl create cluster -f infra/sandbox-eks/cluster.yaml
aws eks update-kubeconfig --region ap-south-1 --name knull-sandbox \
  --alias knull-sandbox-admin --kubeconfig <private-directory>/sandbox-admin.kubeconfig
kubectl --kubeconfig <private-directory>/sandbox-admin.kubeconfig \
  --context knull-sandbox-admin get nodes
eksctl delete cluster -f infra/sandbox-eks/cluster.yaml
```

## Runner identity

After the cluster is ready, apply the runner service account and its limited
RBAC to the **new sandbox context only**:

```sh
kubectl --kubeconfig <private-directory>/sandbox-admin.kubeconfig \
  --context knull-sandbox-admin apply -f infra/sandbox-eks/system-network-policy.yaml
kubectl --kubeconfig <private-directory>/sandbox-admin.kubeconfig \
  --context knull-sandbox-admin apply -f infra/sandbox-eks/runner-rbac.yaml
python3 infra/sandbox-eks/make-runner-kubeconfig.py \
  --profile "$AWS_PROFILE" \
  --admin-kubeconfig <private-directory>/sandbox-admin.kubeconfig \
  --admin-context knull-sandbox-admin \
  --output <private-directory>/sandbox-runner.kubeconfig
```

The generated kubeconfig has one cluster, one context, and a one-hour service
account token. Keep it outside this repository and regenerate it when it
expires. For a persistent deployment, use a rotating sandbox-only credential
provider; a one-hour token is suitable only for the live verification session.
Set `KNULL_SANDBOX_KUBECONFIG` to this file and
`KNULL_SANDBOX_CONTEXT=knull-sandbox-runner`. The runner has no root or AWS
credentials. Its Kubernetes RBAC is limited to the resources and verbs its
current implementation uses, but it applies to all namespaces in this
dedicated cluster because Kubernetes RBAC cannot restrict namespace creation
by name prefix.

The kube-system policy is needed because strict CNI mode also starts CoreDNS
and metrics-server with no network access. Their API connectivity is required
for healthy add-ons and namespace cleanup. It allows traffic only for
`kube-system` pods; each `knull-run-*` namespace keeps its own default-deny
policy.

## Live verification (2026-09-26)

The Mumbai EKS 1.34 cluster had one Ready `m5.large` worker. The VPC CNI
add-on was active with network policy enabled in strict mode. The restricted
runner token could read the sandbox cluster UID and create namespaces, but
`kubectl auth can-i get secrets -A` returned `no`. A pinned checkout fixture
run reached two healthy pods; `RunWithCheck` returned `checked` with
`CleanupVerified=true` after the namespace disappeared. The first attempt
found that `kubectl delete --wait` required namespace list permission; the
runner now polls the exact namespace for deletion instead. Before the
`kube-system` allow policy was installed, unhealthy metrics-server API
discovery prevented cleanup; the policy restored CoreDNS and metrics-server.

A separate `knull-netprobe` namespace proved the data-plane rule: a BusyBox
pod could not resolve or fetch `example.com` under default-deny egress, then
fetched it after an allow-egress policy was added. The probe namespace was
deleted. No production cluster was supplied for a cross-cluster API denial
probe; the runner kubeconfig contains only the sandbox endpoint and a
short-lived service-account token issued by this cluster.

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
