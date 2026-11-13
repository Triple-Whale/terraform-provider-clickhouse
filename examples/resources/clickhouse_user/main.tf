variable "clickhouse_admin_password" {
  type      = string
  sensitive = true
}

variable "service_password" {
  type      = string
  sensitive = true
}

provider "clickhouse" {
  host            = "clickhouse-1.internal"
  port            = 9000
  username        = "provisioner"
  password        = var.clickhouse_admin_password
  verify_replicas = true
  replica_hosts   = ["clickhouse-1.internal", "clickhouse-2.internal"]
}

resource "clickhouse_role" "reader" {
  name    = "service-reader"
  cluster = "main"
  grants = [
    {
      database   = "analytics"
      table      = "*"
      privileges = ["SELECT"]
    },
  ]
}

resource "clickhouse_user" "service" {
  name          = "service-user"
  cluster       = "main"
  password      = var.service_password
  roles         = [clickhouse_role.reader.name]
  default_roles = [clickhouse_role.reader.name]
  grants = [
    {
      database   = "analytics"
      table      = "events"
      privileges = ["INSERT"]
    },
  ]
}
