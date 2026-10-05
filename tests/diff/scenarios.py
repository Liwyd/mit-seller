"""Conversations replayed against both bots. Each function gets the Runner."""
import hashlib
import hmac
import json
import time
import os

from run import ADMIN, SECRET, cb, msg

ROOT = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
TXT = json.load(open(os.path.join(ROOT, "internal/text/text.json")))


def T(path):
    v = TXT
    for p in path.split("."):
        v = v[p]
    return v


U1, U2, U3, U4 = "7000000001", "7000000002", "7000000003", "7000000004"


def last_order(r, side, uid):
    return r.q(side, "SELECT id_order FROM Payment_report WHERE id_user = %s ORDER BY id DESC LIMIT 1", uid)


def last_service(r, side, uid):
    return r.q(side, "SELECT username FROM invoice WHERE id_user = %s AND Status = 'active' ORDER BY CAST(time_sell AS UNSIGNED) DESC, username LIMIT 1", uid)


def basics(r):
    r.step("new user /start", msg(U1, "/start", username="ali"))
    r.step("/start again", msg(U1, "/start", username="ali"))
    r.step("account", msg(U1, "👤 حساب کاربری", username="ali") if False else msg(U1, r_text(r, "text_account"), username="ali"))
    r.step("support", msg(U1, r_text(r, "text_support"), username="ali"))
    r.step("faq callback", cb(U1, "fqQuestions", username="ali"))
    r.step("support message start", cb(U1, "support", username="ali"))
    r.step("support message text", msg(U1, "سلام مشکل دارم", username="ali"))
    r.step("help (disabled)", msg(U1, r_text(r, "text_help"), username="ali"))
    r.step("tariff list", msg(U1, r_text(r, "text_Tariff_list"), username="ali"))
    r.step("affiliates (off)", msg(U1, T("users.affiliates.btn"), username="ali"))
    r.step("unknown text", msg(U1, "hello", username="ali"))
    r.step("back home", msg(U1, T("users.backhome"), username="ali"))
    r.step("closelist", cb(U1, "closelist", username="ali"))
    r.step("services empty", msg(U1, r_text(r, "text_Purchased_services"), username="ali"))
    r.step("/renew empty", msg(U1, "/renew", username="ali"))
    r.step("/status empty", msg(U1, "/status", username="ali"))


def r_text(r, key):
    return r.q("php", "SELECT text FROM textbot WHERE id_text = %s", key)


def buy_after_card(r):
    """Buy with no balance -> card to card receipt -> admin approves -> service delivered."""
    r.step("U2 start", msg(U2, "/start", username="reza"))
    r.step("buy", msg(U2, r_text(r, "text_sell"), username="reza"))
    r.step("pick panel", cb(U2, "location_1", username="reza"))
    r.step("pick category", cb(U2, "categorylist_1", username="reza"))
    r.step("pick product", cb(U2, "prodcutservice_aa01", username="reza"))
    r.step("pay (no balance)", cb(U2, "confirmandgetservice", username="reza"))
    r.step("card to card", cb(U2, "cart_to_offline", username="reza"))
    r.step("receipt text (invalid)", msg(U2, "واریز کردم", username="reza"))
    r.step("receipt photo", msg(U2, photo=True, caption="رسید", username="reza"))
    r.step("admin approves", lambda side: cb(ADMIN, "Confirm_pay_" + last_order(r, side, U2), caption="رسید"))
    r.step("admin approves again", lambda side: cb(ADMIN, "Confirm_pay_" + last_order(r, side, U2), caption="رسید"))
    r.step("services list", msg(U2, r_text(r, "text_Purchased_services"), username="reza"))
    r.step("service card", lambda side: cb(U2, "product_" + last_service(r, side, U2), username="reza"))
    r.step("sub link QR", lambda side: cb(U2, "subscriptionurl_" + last_service(r, side, U2), username="reza"))
    r.step("next page", cb(U2, "next_page", username="reza"))
    r.step("previous page", cb(U2, "previous_page", username="reza"))


def wallet(r):
    r.step("U3 start", msg(U3, "/start", username="sara"))
    r.step("wallet", msg(U3, r_text(r, "text_Add_Balance"), username="sara"))
    r.step("amount bad", msg(U3, "abc", username="sara"))
    r.step("amount small", msg(U3, "100", username="sara"))
    r.step("amount ok", msg(U3, "300000", username="sara"))
    r.step("card", cb(U3, "cart_to_offline", username="sara"))
    r.step("receipt", msg(U3, photo=True, username="sara"))
    r.step("admin rejects", lambda side: cb(ADMIN, "reject_pay_" + last_order(r, side, U3)))
    r.step("reject reason", msg(ADMIN, "رسید نامعتبر"))
    r.step("wallet again", msg(U3, r_text(r, "text_Add_Balance"), username="sara"))
    r.step("amount", msg(U3, "300000", username="sara"))
    r.step("card again", cb(U3, "cart_to_offline", username="sara"))
    r.step("receipt again", msg(U3, photo=True, username="sara"))
    r.step("admin approves", lambda side: cb(ADMIN, "Confirm_pay_" + last_order(r, side, U3)))
    r.step("gift code button", cb(U3, "Discount", username="sara"))
    r.step("gift code wrong", msg(U3, "NOPE", username="sara"))
    r.step("gift code", cb(U3, "Discount", username="sara"))
    r.step("gift code ok", msg(U3, "GIFT", username="sara"))
    r.step("gift code reuse", cb(U3, "Discount", username="sara"))
    r.step("gift code reuse 2", msg(U3, "GIFT", username="sara"))
    # buy from balance with the OFF20 discount
    r.step("buy", msg(U3, r_text(r, "text_sell"), username="sara"))
    r.step("panel", cb(U3, "location_1", username="sara"))
    r.step("category", cb(U3, "categorylist_2", username="sara"))
    r.step("product", cb(U3, "prodcutservice_aa02", username="sara"))
    r.step("discount button", cb(U3, "aptdc", username="sara"))
    r.step("discount code", msg(U3, "OFF20", username="sara"))
    r.step("pay with discount", cb(U3, "confirmandgetserviceDiscount", username="sara"),
           expect_diff="index.php counted the use of code 'dis' instead of OFF20 and left the marker set; Go counts OFF20",
           reconcile=["UPDATE DiscountSell SET usedDiscount = '1' WHERE codeDiscount = 'OFF20'",
                      "UPDATE user SET Processing_value_four = '0' WHERE id = '%s'" % U3])
    r.step("buy again full price", msg(U3, r_text(r, "text_sell"), username="sara"))
    r.step("panel 2", cb(U3, "location_1", username="sara"))
    r.step("category 2", cb(U3, "categorylist_1", username="sara"))
    r.step("product 2", cb(U3, "prodcutservice_aa01", username="sara"))
    r.step("pay", cb(U3, "confirmandgetservice", username="sara"))
    r.step("account", msg(U3, r_text(r, "text_account"), username="sara"))
    r.step("service card", lambda side: cb(U3, "product_" + last_service(r, side, U3), username="sara"))
    r.step("extend list", lambda side: cb(U3, "extend_" + last_service(r, side, U3), username="sara"))
    r.step("extend pick", lambda side: cb(U3, "serviceextendselect_aa01", username="sara"))
    r.step("extend confirm", cb(U3, "confirmserivce-aa01", username="sara"))
    r.step("extra volume", lambda side: cb(U3, "Extra_volume_" + last_service(r, side, U3), username="sara"))
    r.step("extra volume bad", msg(U3, "x", username="sara"))
    r.step("extra volume 2 GB", msg(U3, "2", username="sara"))
    r.step("extra confirm", cb(U3, "confirmaextra_2", text="فاکتور", username="sara"))
    r.step("change link", lambda side: cb(U3, "changelink_" + last_service(r, side, U3), username="sara"))
    r.step("change link confirm", lambda side: cb(U3, "confirmchange_" + last_service(r, side, U3), username="sara"))
    r.step("remove request", lambda side: cb(U3, "removeserviceuserco-" + last_service(r, side, U3), username="sara"))
    r.step("remove request confirm", lambda side: cb(U3, "confirmremoveservices-" + last_service(r, side, U3), username="sara"))
    r.step("admin accepts removal", lambda side: cb(ADMIN, "remoceserviceadmin-" + last_service(r, side, U3)))
    r.step("refund amount", msg(ADMIN, "10000"))


def trial(r):
    r.step("U4 start", msg(U4, "/start"))
    r.step("trial button", msg(U4, r_text(r, "text_usertest")))
    r.step("trial on Germany", cb(U4, "locationtests_1"))
    r.step("trial again (limit)", msg(U4, r_text(r, "text_usertest")))
    r.step("admin raises limit", cb(ADMIN, "limitusertest_" + U4))
    r.step("limit value", msg(ADMIN, "2"))
    r.step("trial on Mit (choice)", cb(U4, "locationtests_2"))
    r.step("choose random", cb(U4, "usernamechoice_random_test"),
           expect_diff=None)
    r.step("trial #3 (limit)", msg(U4, r_text(r, "text_usertest")))


def admin(r):
    A = ADMIN
    r.step("admin /start", msg(A, "/start"))
    r.step("admin panel", msg(A, "/panel"))
    r.step("stats", msg(A, T("Admin.keyboardadmin.bot_statistics")),
           expect_diff="PHP showed rowCount() of an aggregate (always 1) as the day's sales; Go counts them")
    r.step("shop", msg(A, T("Admin.keyboardadmin.shop_section")))
    r.step("add category", msg(A, T("Admin.category.add")))
    r.step("category name", msg(A, "ویژه"))
    r.step("add product", msg(A, T("Admin.Product.addproduct")))
    r.step("product name", msg(A, "محصول تست"))
    r.step("product location", msg(A, "Germany"))
    r.step("product category", msg(A, "ویژه"))
    r.step("product volume bad", msg(A, "ده"))
    r.step("product volume", msg(A, "10"))
    r.step("product days", msg(A, "15"))
    r.step("product price", msg(A, "25000"))
    r.step("edit product", msg(A, T("Admin.Product.titlebtnedit")))
    r.step("edit pick location", msg(A, "Germany"))
    r.step("edit pick product", msg(A, "محصول تست"))
    r.step("edit price", msg(A, T("Admin.Product.editprice")))
    r.step("new price", msg(A, "27000"))
    r.step("remove product", msg(A, T("Admin.Product.titlebtnremove")))
    r.step("remove location", msg(A, "Germany"))
    r.step("remove name", msg(A, "محصول تست"))
    r.step("gift code create", msg(A, T("Admin.Discount.titlebtn")))
    r.step("gift code", msg(A, "NEWGIFT"))
    r.step("gift price", msg(A, "5000"))
    r.step("sell discount create", msg(A, T("Admin.Discountsell.create")))
    r.step("sell discount code", msg(A, "HALF50"))
    r.step("sell discount pct", msg(A, "50"))
    r.step("sell discount limit", msg(A, "3"))
    r.step("sell discount first", msg(A, "0"))
    r.step("text settings", msg(A, T("Admin.keyboardadmin.bot_text_settings")))
    r.step("change start text", msg(A, T("Admin.changetext.textstart")))
    r.step("new start text", msg(A, "به ربات ما خوش آمدید"))
    r.step("change buy button", msg(A, T("users.changetext.buy_subscription_button")))
    r.step("new buy label", msg(A, "🛒 خرید"))
    r.step("settings", msg(A, T("Admin.keyboardadmin.settings")))
    why = "PHP without shell_exec (as in this test) always shows auto-confirm as on; Go keeps its own switch"
    r.step("status settings", msg(A, T("Admin.keyboardadmin.seetingstatus")), expect_diff=why)
    r.step("toggle help on", cb(A, "editstsuts-help_Status-0"), expect_diff=why)
    r.step("toggle category off", cb(A, "editstsuts-category-1"), expect_diff=why)
    r.step("finance", msg(A, T("Admin.keyboardadmin.finance")))
    r.step("toggle nowpayments", cb(A, "editpay-nowpayment-offnowpayment"))
    r.step("card number", msg(A, T("users.moeny.card_number_settings")))
    r.step("card value", msg(A, "6037-9911-2233-4455 به نام تست"))
    r.step("user search", msg(A, T("Admin.keyboardadmin.user_search")))
    r.step("user id", msg(A, U3),
           expect_diff="PHP showed rowCount() of COUNT(*) (always 1) as the service count; Go shows the real count")
    r.step("add balance", cb(A, "addbalanceuser_" + U3))
    r.step("amount", msg(A, "12000"))
    r.step("block", cb(A, "banuserlist_" + U3))
    r.step("block reason", msg(A, "تست"))
    r.step("blocked user writes", msg(U3, "/start", username="sara"))
    r.step("unblock", cb(A, "unbanuserr_" + U3))
    r.step("admins list", msg(A, T("Admin.manageadmin.showlistbtn")))
    r.step("help section", msg(A, T("Admin.Help.titlebtn")))
    r.step("add help", msg(A, T("Admin.Help.addhelp")))
    r.step("help name", msg(A, "اندروید"))
    r.step("help text", msg(A, "برنامه v2rayNG را نصب کنید"))
    r.step("user opens help", msg(U1, r_text(r, "text_help"), username="ali"))
    r.step("user picks help", msg(U1, "اندروید", username="ali"))
    r.step("add panel without secret", msg(A, T("Admin.keyboardadmin.add_panel")))
    r.step("wrong secret", msg(A, "nope"))
    r.step("manage panel", msg(A, T("Admin.keyboardadmin.manage_panel")))
    r.step("right secret", msg(A, SECRET))
    r.step("pick panel", msg(A, "Germany"))
    r.step("panel status", msg(A, T("Admin.managepanel.btnshowconnect")))
    r.step("panel test visibility", msg(A, T("Admin.managepanel.showpaneltestbtn")))
    r.step("toggle test visibility", cb(A, "ontestshowpanel"))
    r.step("admin back", msg(A, T("Admin.Back-Adminment")))


def autopay(r):
    A = ADMIN
    r.step("autopay menu", msg(A, "💳 تأیید خودکار پرداخت"))
    r.step("autopay on", cb(A, "autopay_toggle"))
    r.step("wallet", msg(U1, r_text(r, "text_Add_Balance"), username="ali"))
    r.step("amount", msg(U1, "200000", username="ali"))
    r.step("card (reserved amount)", cb(U1, "cart_to_offline", username="ali"))

    cache = {}

    def sms(side):
        if side in cache:
            return cache[side]
        amount = r.q(side, "SELECT amount FROM autopay_order WHERE id_user = %s AND status = 'open' ORDER BY id DESC LIMIT 1", U1)
        body = "بانک ملت\nواریز:%s ریال\nمانده:9,000,000 ریال" % "{:,}".format(int(amount) * 10)
        cache[side] = json.dumps({"action": "sms", "sender": "MELLAT", "body": body, "sent_at": "2026-10-03 12:00:00", "nonce": "1", "device": "Test Phone"}, ensure_ascii=False).encode()
        return cache[side]

    def sign(side, body):
        key = r.q(side, "SELECT device_key FROM autopay LIMIT 1")
        return {"Content-Type": "application/json", "X-Autopay-Signature": hmac.new(key.encode(), body, hashlib.sha256).hexdigest()}

    r.step("phone forwards the deposit SMS", sms, path="/autopay.php", headers=sign, compare_body=True,
           expect_diff="after an automatic top-up Go also clears the customer's pending amount, like the admin button does",
           reconcile=["UPDATE user SET Processing_value = '0' WHERE id = '%s'" % U1])
    r.step("same SMS again", sms, path="/autopay.php", headers=sign, compare_body=True)
    r.step("bad signature", lambda side: b'{"action":"ping"}', path="/autopay.php", headers={"X-Autopay-Signature": "00"}, compare_body=True)
    r.step("autopay recent sms", cb(A, "autopay_sms"))
    r.step("autopay off", cb(A, "autopay_toggle"),
           expect_diff="autopay.php stored last_seen in the server's zone (UTC) and the admin page read it as Tehran time (\"3 hours ago\"); Go uses Tehran throughout")
    r.step("autopay orders", cb(A, "autopay_orders"))
    # the reserved amount is random (unique per order), so the credited balance differs
    r.sql_both("UPDATE user SET Balance = 203000 WHERE id = '%s'" % U1)


U5, U6, U7 = "7000000005", "7000000006", "7000000007"


def mit(r):
    # paid purchase on the Mit panel with a plain random username
    r.sql_both("UPDATE marzban_panel SET MethodUsername = '%s' WHERE name_panel = 'MitDE'" % T("users.customidAndRandom"))
    r.sql_both("UPDATE setting SET statuscategory = '0'")
    r.step("U5 start", msg(U5, "/start", username="nima"))
    r.sql_both("UPDATE user SET Balance = 500000 WHERE id = '%s'" % U5)
    r.step("buy", msg(U5, r_text(r, "text_sell"), username="nima"))
    r.step("Mit panel", cb(U5, "location_2", username="nima"))
    r.step("product", cb(U5, "prodcutservice_aa03", username="nima"))
    r.step("pay", cb(U5, "confirmandgetservice", username="nima"))
    # "days left" is floor(diff/86400)+1: shown in the creation second it reads 31,
    # a second later 30. Wait so both bots look at it a second later.
    r.step("service card", lambda side: (time.sleep(1.1), cb(U5, "product_" + last_service(r, side, U5), username="nima"))[1])
    r.step("config list", lambda side: cb(U5, "config_" + last_service(r, side, U5), username="nima"))
    r.step("extend list", lambda side: cb(U5, "extend_" + last_service(r, side, U5), username="nima"))
    r.step("extend pick", cb(U5, "serviceextendselect_aa03", username="nima"))
    r.step("extend ok", cb(U5, "confirmserivce-aa03", username="nima"))
    r.step("extend list 2", lambda side: cb(U5, "extend_" + last_service(r, side, U5), username="nima"))
    r.step("extend pick 2", cb(U5, "serviceextendselect_aa03", username="nima"))
    http_fail(1)
    r.step("extend refused by Mit", cb(U5, "confirmserivce-aa03", username="nima"),
           expect_diff="Mit refused the renewal: PHP kept the customer's money and said it was renewed, Go refunds it and tells the admins",
           reconcile=["UPDATE user SET Balance = 340000 WHERE id = '%s'" % U5])
    r.step("extra volume", lambda side: cb(U5, "Extra_volume_" + last_service(r, side, U5), username="nima"))
    r.step("extra 3 GB", msg(U5, "3", username="nima"))
    http_fail(1)
    r.step("extra refused by Mit", cb(U5, "confirmaextra_3", text="فاکتور", username="nima"))
    r.step("change link (unsupported)", lambda side: cb(U5, "confirmchange_" + last_service(r, side, U5), username="nima"))
    r.step("admin opens Mit panel", msg(ADMIN, T("Admin.keyboardadmin.manage_panel")))
    r.step("secret", msg(ADMIN, SECRET))
    r.step("pick", msg(ADMIN, "MitDE"))
    r.step("connection", msg(ADMIN, T("Admin.managepanel.btnshowconnect")))
    r.step("edit creds menu", msg(ADMIN, T("Admin.managepanel.keyboardpanel.editmitcreds")))
    r.step("edit mit password", cb(ADMIN, "editmitcred_password_panel"))
    r.step("new password", msg(ADMIN, "pass2"),
           expect_diff="Go also drops the cached tokens of the old credentials", reconcile=["UPDATE marzban_panel SET datelogin = NULL"])
    r.step("user removes inactive service", lambda side: cb(U5, "removebyuser-" + last_service(r, side, U5), username="nima"))


def http_fail(n):
    import urllib.request
    from run import MOCK_PHP, MOCK_GO
    for port in (MOCK_PHP, MOCK_GO):
        urllib.request.urlopen(urllib.request.Request("http://127.0.0.1:%d/__fail/%d" % (port, n), data=b"", method="POST"))


def referral(r):
    A = ADMIN
    r.step("affiliate settings", msg(A, T("Admin.keyboardadmin.affiliate_settings")))
    r.step("affiliate status", msg(A, T("Admin.affiliate.status")))
    r.step("turn on", cb(A, "offaffiliates"))
    r.step("percentage", msg(A, T("Admin.affiliate.Percentageset")))
    r.step("10%", msg(A, "10"))
    r.step("start gift", msg(A, T("Admin.affiliate.giftstart")))
    r.step("gift amount", msg(A, "3000"))
    r.step("affiliates page", msg(U1, T("users.affiliates.btn"), username="ali"))
    r.step("U6 joins by ref code", lambda side: msg(U6, "/start " + r.q(side, "SELECT ref_code FROM user WHERE id = %s", U1), username="mina"))
    r.step("U6 again with code", lambda side: msg(U6, "/start " + r.q(side, "SELECT ref_code FROM user WHERE id = %s", U1), username="mina"))
    r.step("U7 joins by numeric id", msg(U7, "/start " + U1))
    r.step("U7 self ref", msg(U7, "/start " + U7))
    r.sql_both("UPDATE user SET Balance = 100000 WHERE id = '%s'" % U6)
    r.step("U6 buys", msg(U6, r_text(r, "text_sell"), username="mina"))
    r.step("panel", cb(U6, "location_1", username="mina"))
    r.step("product", cb(U6, "prodcutservice_aa01", username="mina"))
    r.step("pay (commission to U1)", cb(U6, "confirmandgetservice", username="mina"))


def gates(r):
    A = ADMIN
    r.step("set channel", msg(A, T("Admin.channel.setting")))
    r.step("change channel", msg(A, T("Admin.channel.changechannelbtn")))
    r.step("channel name", msg(A, "mitchannel"))
    r.step("user with channel (member)", msg(U1, "/start", username="ali"))
    r.step("rules on", cb(A, "editstsuts-roll_Status-0"), expect_diff="auto-confirm row (see above)")
    r.step("user sees rules", msg(U7, "/start"))
    r.step("user accepts rules", msg(U7, T("users.rulesaccept")))
    r.step("rules off", cb(A, "editstsuts-roll_Status-1"), expect_diff="auto-confirm row (see above)")
    r.step("number check on", cb(A, "editstsuts-get_number-0"), expect_diff="auto-confirm row (see above)")
    r.step("buy asks for number", msg(U7, r_text(r, "text_sell")))
    r.step("text instead of contact", msg(U7, "0912"))
    r.step("someone else's contact", msg(U7, contact=("989121111111", U1)))
    r.step("own contact", msg(U7, contact=("989121234567", U7)))
    r.step("number check off", cb(A, "editstsuts-get_number-1"), expect_diff="auto-confirm row (see above)")
    r.step("bot off", cb(A, "editstsuts-statusbot-1"), expect_diff="auto-confirm row (see above)")
    r.step("user while bot off", msg(U7, "/start"))
    r.step("admin while bot off", msg(A, "/start"))
    r.step("bot on", cb(A, "editstsuts-statusbot-0"), expect_diff="auto-confirm row (see above)")
    r.step("verify on", cb(A, "editstsuts-verify-0"), expect_diff="auto-confirm row (see above)")
    r.step("new user unverified", msg("7000000099", "/start"))
    r.step("admin verifies", cb(A, "verify_7000000099"))
    r.step("verified user", msg("7000000099", "/start"))
    r.step("verify off", cb(A, "editstsuts-verify-1"), expect_diff="auto-confirm row (see above)")
    r.step("remove channel", msg(A, T("Admin.channel.changechannelbtn")))
    r.step("empty channel", msg(A, " "))


def admin2(r):
    A = ADMIN
    r.step("send to one user", msg(A, T("Admin.systemsms.sendmessageauser")))
    r.step("message text", msg(A, "پیام تست"))
    r.step("unknown id", msg(A, "123"))
    r.step("user id", msg(A, U1))
    r.step("answer support", cb(A, "Response_" + U1))
    r.step("answer text", msg(A, "پاسخ پشتیبانی"))
    r.step("remove service by admin", msg(A, T("Admin.ManageUser.removeorderbtn")))
    r.step("service name", lambda side: msg(A, last_service(r, side, U2)))
    r.step("search order", msg(A, T("Admin.ManageUser.searchorder")))
    r.step("order name", lambda side: msg(A, r.q(side, "SELECT username FROM invoice WHERE id_user = %s ORDER BY price_product + 0 DESC LIMIT 1", U3)))
    r.step("manual order", cb(A, "addordermanualـ" + U1))
    r.step("manual username", msg(A, "manualuser1"))
    r.step("manual panel", msg(A, "Germany"))
    r.step("manual product", msg(A, "۳۰ گیگ یک ماهه"))
    r.step("view orders", cb(A, "vieworderall_" + U1))
    r.step("test account settings", msg(A, T("Admin.keyboardadmin.test_account_settings")))
    r.step("test time", msg(A, T("Admin.Usertest.settimeusertest")))
    r.step("test time value", msg(A, "3"))
    r.step("test volume", msg(A, T("Admin.Usertest.setvolumeusertest")))
    r.step("test volume small", msg(A, "50"))
    r.step("test volume value", msg(A, "300"))
    r.step("extra price", msg(A, T("Admin.managepanel.keyboardpanel.setvolume")))
    r.step("extra price value", msg(A, "6000"))
    r.step("balance to all", msg(A, T("Admin.Balance.SendBalanceAll")))
    r.step("balance all value", msg(A, "1000"))
    r.step("remove gift code", msg(A, T("Admin.Discount.titlebtnremove")))
    r.step("gift code name", msg(A, "NEWGIFT"))
    r.step("cron menu", msg(A, T("Admin.keyboardadmin.settingscron")))
    r.step("remove days", msg(A, T("Admin.cron.remove.timeset")))
    r.step("remove days value", msg(A, "3"))
    r.step("report channel", msg(A, T("Admin.channel.channelreport")))
    r.step("report channel value", msg(A, "-1002"), expect_diff="PHP sent the test message to the previous channel")
    r.step("method username", msg(A, T("Admin.keyboardadmin.manage_panel")))
    r.step("secret", msg(A, SECRET))
    r.step("pick Germany", msg(A, "Germany"))
    r.step("method menu", msg(A, T("Admin.managepanel.methodusername")))
    r.step("custom text + random", msg(A, T("users.customtextandrandom")))
    r.step("custom name", msg(A, "mit"))
    r.step("sub link status", msg(A, T("Admin.managepanel.sublinkstatus")))
    r.step("sub link off", cb(A, "onsublink"))
    r.step("config status", msg(A, T("Admin.managepanel.configstatus")))
    r.step("config on", cb(A, "offconfig"))
    r.step("on hold", msg(A, T("Admin.managepanel.keyboardpanel.on_hold_status")))
    r.step("on hold on", cb(A, "offonhold"))
    r.sql_both("UPDATE user SET Balance = 200000 WHERE id = '%s'" % U7)
    r.step("U7 buys (configs, on hold)", msg(U7, r_text(r, "text_sell")))
    r.step("panel", cb(U7, "location_1"))
    r.step("product", cb(U7, "prodcutservice_aa01"))
    r.step("pay", cb(U7, "confirmandgetservice"))
    r.step("rename panel", msg(A, T("Admin.managepanel.keyboardpanel.namepanel")))
    r.step("new name", msg(A, "Germany2"))
    r.step("remove panel", msg(A, T("Admin.managepanel.keyboardpanel.removepanel")))
    r.step("broadcast", msg(A, T("Admin.systemsms.sendbulkbtn")))
    r.step("broadcast text", msg(A, "اطلاعیه"))
    r.step("broadcast confirm", msg(A, T("Admin.accept")))
    r.step("broadcast cancel", cb(A, "cancel_sendmessage"))
    r.step("add admin", msg(A, T("Admin.Addedadmin")))
    r.step("admin id", msg(A, "5000000002"))
    r.step("remove admin", msg(A, T("Admin.Removeedadmin")))
    r.step("main admin cannot go", msg(A, ADMIN))
    r.step("remove admin", msg(A, T("Admin.Removeedadmin")))
    r.step("other admin", msg(A, "5000000002"))



ORDER = ["basics", "buy_after_card", "wallet", "trial", "admin", "autopay", "mit", "referral", "gates", "admin2"]
