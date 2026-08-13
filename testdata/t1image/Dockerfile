# Fixture image for docker-gated T1 tests (docs/design/M1c-containers-
# parallel.md "Fixture image & docker test guard"). Built ONCE per machine
# by the test helpers / scripts/accept.sh when absent, with context
# testdata/ so the fake agent fixtures ride inside:
#
#   docker build -f testdata/t1image/Dockerfile -t multiverso-t1-fixture:v1 testdata
#
# Never pushed; bump the tag version when this file or the fixtures change.
# bash because the fake agents are bash (alpine ships ash); pytest because
# the toyrepo oracle is `python3 -m pytest -q`. No test pulls anything
# larger than python:3.12-alpine.
FROM python:3.12-alpine
RUN apk add --no-cache bash && pip install --no-cache-dir pytest==8.*
COPY fakeagent/claude fakeagent/codex /usr/local/bin/
