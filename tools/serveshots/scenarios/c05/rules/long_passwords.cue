import "pravo.dev/pravo"

long_passwords: pravo.#Policy & {
	description: "A password has at least sixteen characters."
	rule: changes: [...{
		type: string
		if type == "random_password" {
			after?: length: >=16
		}
	}]
}
