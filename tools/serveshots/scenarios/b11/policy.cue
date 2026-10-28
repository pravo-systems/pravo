package main

import "pravo.dev/pravo"

no_delete: pravo.#Policy & {
	description: "A plan may create and change. It may not delete."
	rule: {
		changes: [...{
			addr:   string
			mode:   string
			action: string
			if mode == "managed" && action == "delete" {
				error("\(addr): delete is not allowed")
			}
		}]
	}
}
