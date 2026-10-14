package digitalteacher

// Simli's official adult preset catalog, verified 2026-10-14:
// https://docs.simli.com/api-reference/preset-faces
var simliPresets = []struct{ id, name, image, voice string }{
	{SimliAvatar, "Tina", "asian_woman.png", "warm"},
	{"b9e5fba3-071a-4e35-896e-211c4d6eaa7b", "Laila", "indian_woman_2.png", "gentle"},
	{"d2a5c7c6-fed9-4f55-bcb3-062f7cd20103", "Kate", "white_woman.png", "warm"},
	{"7e74d6e7-d559-4394-bd56-4923a3ab75ad", "Sabour", "indian_man.png", "bright"},
	{"1c6aa65c-d858-4721-a4d9-bda9fde03141", "Fred", "black_man.png", "steady"},
	{"5fc23ea5-8175-4a82-aaaf-cdd8c88543dc", "Madison", "madison.png", "gentle"},
	{"804c347a-26c9-4dcf-bb49-13df4bed61e8", "Mark", "black_programmer.png", "bright"},
	{"afdb6a3e-3939-40aa-92df-01604c23101c", "Zahra", "zahra.png", "warm"},
	{"dd10cb5a-d31d-4f12-b69f-6db3383c006e", "Hank", "hank.png", "story"},
	{"c65af549-9105-442a-92a3-dc6c89e34149", "DJ", "dj_real.png", "lively"},
	{"f0ba4efe-7946-45de-9955-c04a04c367b9", "Doctor", "doctor.png", "steady"},
	{"b1f6ad8f-ed78-430b-85ef-2ec672728104", "Charlotte", "Charlotte.png", "gentle"},
}
