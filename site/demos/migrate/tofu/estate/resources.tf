data "local_file" "banner" {
  filename = "banner.txt"
}

resource "random_pet" "server" {
  prefix = local.prefix
  length = 2
}

resource "local_file" "hello" {
  filename = "out/hello.txt"
  content  = "${data.local_file.banner.content}Hello, ${random_pet.server.id}!\n"

  depends_on = [random_integer.port]

  lifecycle {
    ignore_changes = [file_permission]
  }
}
