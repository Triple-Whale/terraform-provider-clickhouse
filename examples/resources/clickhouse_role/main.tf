variable "clickhouse_host" {
  type = string
}

variable "clickhouse_replica_hosts" {
  type = list(string)
}

variable "clickhouse_username" {
  type = string
}

variable "clickhouse_password" {
  type      = string
  sensitive = true
}

provider "clickhouse" {
  host            = var.clickhouse_host
  port            = 9000
  username        = var.clickhouse_username
  password        = var.clickhouse_password
  verify_replicas = true
  replica_hosts   = var.clickhouse_replica_hosts
}

resource "clickhouse_role" "orders_reader" {
  name    = "orders_reader"
  cluster = "main"
  grants = [
    {
      database   = "sonic_system"
      table      = "orders"
      privileges = ["SELECT"]
    },
    {
      database   = "service_events"
      table      = "*"
      privileges = ["INSERT"]
    },
  ]
}
