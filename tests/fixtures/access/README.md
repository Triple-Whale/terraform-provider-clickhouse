# Isolated access-resource tests

This fixture runs two ClickHouse 26.3.62.3 servers and Keeper. The SQL cluster is
`main`. Its unauthenticated administrative account is for this disposable fixture
only; published ports are bound to localhost.

From the repository root:

```sh
docker compose -f tests/fixtures/access/compose.yaml up -d --wait
docker build -f tests/fixtures/access/Dockerfile -t clickhouse-access-go .
docker run --rm --network clickhouse-access-tests_default \
  -v "$PWD:/src:ro" \
  -e TF_ACC=1 \
  -e TF_CLICKHOUSE_HOST=ch1 \
  -e TF_CLICKHOUSE_PORT=9000 \
  -e TF_CLICKHOUSE_USERNAME=default \
  -e TF_CLICKHOUSE_PASSWORD= \
  -e TF_CLICKHOUSE_CLUSTER=main \
  -e TF_CLICKHOUSE_REPLICA_HOSTS=ch1,ch2 \
  clickhouse-access-go go test -v ./pkg/resources/user ./pkg/resources/role
```

For the existing single-server resource acceptance tests, omit the cluster and
replica environment variables and run their packages with the same container.
Host-side native clients can connect to `127.0.0.1:19000` and `127.0.0.1:19001`;
HTTP is available on `127.0.0.1:18123` and `127.0.0.1:18124`.

After reviewing the results, stop the fixture with:

```sh
docker compose -f tests/fixtures/access/compose.yaml down
```
