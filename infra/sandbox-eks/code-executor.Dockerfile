# The final image is pushed to the customer sandbox registry and referenced
# by its immutable ECR digest. Generated scripts use only Python stdlib.
FROM python:3.12-alpine@sha256:4c47124a8391cb7a9f571164147d154777cf012a4ece5f86097130d7a4478111
RUN addgroup -S runner && adduser -S -G runner -u 10001 runner
USER 10001:10001
WORKDIR /work
CMD ["python3", "--version"]
