terraform {
  required_providers {
    clickhouse = {
      version = "2.0.0"
      source  = "hashicorp.com/ivanofthings/clickhouse"
    }
  }
}

provider "clickhouse" {
  port = 9000
  host           = "stg.sonic-sharded-0-2.clickhouse.internal.whaledb.io"
  username       = "sonic"
  password       = ""
}

/*
resource "clickhouse_table" "replicated_table" {
  database      = "default"
  name          = "kafka_test"
  engine        = "Kafka"
  engine_params  = ["'sonic-cluster-kafka-bootstrap.internal.sonicwhale.io:9092'", "'test'", "'test'", "'JSONEachRows'"]
  column {
    name = "event_date"
    type = "Date"
    nullable = true
  }
  column {
    name = "event_type"
    type = "Int32"
  }
  column {
    name = "article_id"
    type = "Int32"
  }
  column {
    name = "title"
    type = "String"
  }
  
  settings = {
    kafka_thread_per_consumer = 1
    kafka_num_consumers = 8
  }
  
}
*/
/*
resource "clickhouse_table" "t2" {
  database      = "default"
  name          = "Replicated_test"
  engine        = "ReplacingMergeTree"
  engine_params = []

  column {
    name = "event_date"
    type = "Date"
    
  }

    column {
    name = "nnn"
    type = "DateTime"
    default_kind = "DEFAULT"
    default_expression = 7

  }


  order_by = ["event_date"]
  partition_by {
    by = "event_date"
  }
}
*/
/*
resource "clickhouse_table" "test" {
  database      = "default"
  name          = "test"
  engine        = "ReplacingMergeTree"
  column {
    name = "event_date"
    
    type = "Date"
  }
  order_by = ["event_date"]
}
*/
/*
resource "clickhouse_view" "test_view" {
  database      = "default"
  cluster = "main"
  name          = "test_view"
  materialized = false
  comment = "jesse's"
query = "       select     2     "
}

resource "clickhouse_view" "test_view1" {
  database      = "public"
  name          = "test_view"
  materialized = false
  comment = "jesse's"
query = "select * FROM default.shop_settings limit 10"
}

*/

resource "clickhouse_table" "example" {
  database = "default"
  name     = "example_table"
  engine   = "MergeTree"

  column {
    name = "id"
    type = "UInt32"
  }



  column {
    name = "created_at"
    type = "DateTime"
  }

  order_by = ["id"]
}
