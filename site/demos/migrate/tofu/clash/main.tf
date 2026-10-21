terraform {
  required_providers {
    random = { source = "hashicorp/random", version = "~> 3.6" }
  }
}

resource "random_pet" "region" {
  for_each = toset(["eu"])
  prefix   = each.key
  length   = 2
}

resource "random_pet" "region_eu" {
  prefix = "eu"
  length = 2
}
