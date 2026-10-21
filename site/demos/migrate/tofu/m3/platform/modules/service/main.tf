variable "name" {
  type = string
}

variable "port" {
  type = number

  validation {
    condition     = var.port > 1024 && var.port < 65536
    error_message = "port must be between 1025 and 65535."
  }
}

resource "random_pet" "host" {
  prefix = var.name
  length = 2
}

resource "local_file" "config" {
  filename = "out/${var.name}.conf"
  content  = "host = ${random_pet.host.id}\nport = ${var.port}\n"
}

output "endpoint" {
  value = "${random_pet.host.id}:${var.port}"
}
