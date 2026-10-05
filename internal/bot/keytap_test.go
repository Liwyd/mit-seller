package bot

import "testing"

func TestMenuKey(t *testing.T) {
	same := [][2]string{
		{"🔑 اکانت تست", "اکانت تست"},
		{"اکانت  تست ", "اکانت تست"},
		{"👨🏻‍💻 مشخصات کاربری", "مشخصات کاربری"},
		{"📚 آموزش", "آموزش 📚"},
		{"سرویس‌های من", "سرویس‌های من"},
		{"☎️ پشتیبانی", "پشتیبانی"},
	}
	for _, p := range same {
		if menuKey(p[0]) != menuKey(p[1]) {
			t.Errorf("%q and %q differ: %q / %q", p[0], p[1], menuKey(p[0]), menuKey(p[1]))
		}
	}
	if menuKey("💰 تعرفه اشتراک ها") == menuKey("💰 افزایش موجودی") {
		t.Error("different labels collide")
	}
	if menuKey("🔥✨") != "" {
		t.Error("emoji-only label should be empty")
	}
}
