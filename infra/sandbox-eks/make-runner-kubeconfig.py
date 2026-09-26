#!/usr/bin/env python3
"""Write a short-lived, sandbox-only kubeconfig without exposing its token."""

import argparse
import json
import os
import subprocess
import sys


def command(*args):
    return subprocess.run(args, check=True, capture_output=True, text=True).stdout.strip()


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--profile", required=True, help="AWS profile for cluster lookup")
    parser.add_argument("--admin-context", required=True, help="bootstrap kubectl context")
    parser.add_argument("--output", required=True, help="new kubeconfig path")
    args = parser.parse_args()

    cluster = json.loads(command(
        "aws", "eks", "describe-cluster", "--profile", args.profile,
        "--region", "ap-south-1", "--name", "knull-sandbox", "--output", "json",
    ))["cluster"]
    admin = json.loads(command(
        "kubectl", "config", "view", "--raw", "--minify", "--context",
        args.admin_context, "-o", "json",
    ))
    if admin["clusters"][0]["cluster"]["server"] != cluster["endpoint"]:
        parser.error("admin context does not target the dedicated sandbox cluster")

    token = command(
        "kubectl", "--context", args.admin_context, "-n", "knull-system",
        "create", "token", "sandbox-runner", "--duration=1h",
    )
    kubeconfig = {
        "apiVersion": "v1", "kind": "Config",
        "clusters": [{"name": "knull-sandbox", "cluster": {
            "server": cluster["endpoint"],
            "certificate-authority-data": cluster["certificateAuthority"]["data"],
        }}],
        "users": [{"name": "sandbox-runner", "user": {"token": token}}],
        "contexts": [{"name": "knull-sandbox-runner", "context": {
            "cluster": "knull-sandbox", "user": "sandbox-runner",
        }}],
        "current-context": "knull-sandbox-runner",
    }
    fd = os.open(args.output, os.O_WRONLY | os.O_CREAT | os.O_EXCL, 0o600)
    with os.fdopen(fd, "w") as output:
        json.dump(kubeconfig, output)
        output.write("\n")
    print(f"Wrote {args.output} (token expires in up to one hour)")


if __name__ == "__main__":
    try:
        main()
    except subprocess.CalledProcessError as error:
        print(f"Command failed ({error.returncode}): {' '.join(error.cmd)}", file=sys.stderr)
        print(error.stderr.strip(), file=sys.stderr)
        sys.exit(error.returncode)
