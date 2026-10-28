import "pravo.dev/pravo"

no_destroy: pravo.#Policy & {
	description: "A plan may create and change. It may not destroy."
	rule: summary: destory: 0
}
