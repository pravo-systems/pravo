package estate

import "pravo.dev/pravo"

web_port: pravo.#Output & {value: port.result}
web_file: pravo.#Output & {value: web.filename}
