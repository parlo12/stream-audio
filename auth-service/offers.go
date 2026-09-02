package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt"
	"github.com/stripe/stripe-go/v78"
	"github.com/stripe/stripe-go/v78/checkout/session"
	"github.com/stripe/stripe-go/v78/customer"
)

// Web-only promotional-pricing engine (iOS uses Apple's own offer types).
//
// Flow:
//   • New signups see the ANCHOR price ($18.99 premium) at checkout.
//   • A registered user who has not subscribed within 24h gets one "abandon"
//     email offering the standard floor price ($14.99).
//   • A subscriber who cancels gets, 24h later, one "winback" email at the
//     floor price.
// Both emails link to /offer/:token, a public endpoint that starts a Stripe
// Checkout at the discounted price for that specific user. The floor price is
// the margin-validated $14.99 — the "discount" never dips below it.

// OfferEmail records a sent promotional email so each user gets at most one of
// each kind (unique index on user+kind).
type OfferEmail struct {
	ID     uint      `gorm:"primaryKey"`
	UserID uint      `gorm:"uniqueIndex:idx_offer_user_kind;not null"`
	Kind   string    `gorm:"uniqueIndex:idx_offer_user_kind;not null"` // "abandon" | "winback"
	SentAt time.Time
}

func offerBaseURL() string   { return getEnv("OFFER_BASE_URL", "https://narrafied.com") }
func anchorDisplay() string  { return getEnv("OFFER_ANCHOR_PRICE_DISPLAY", "$18.99") }
func discountDisplay() string { return getEnv("OFFER_DISCOUNT_PRICE_DISPLAY", "$14.99") }

// offerToken mints a 14-day signed link token identifying the user and offer.
func offerToken(userID uint, kind string) (string, error) {
	claims := jwt.MapClaims{
		"user_id": userID,
		"kind":    kind,
		"purpose": "offer",
		"exp":     time.Now().Add(14 * 24 * time.Hour).Unix(),
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(jwtSecretKey)
}

func parseOfferToken(tok string) (uint, string, error) {
	parsed, err := jwt.Parse(tok, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, fmt.Errorf("unexpected signing method")
		}
		return jwtSecretKey, nil
	})
	if err != nil || !parsed.Valid {
		return 0, "", fmt.Errorf("invalid token")
	}
	claims, ok := parsed.Claims.(jwt.MapClaims)
	if !ok || claims["purpose"] != "offer" {
		return 0, "", fmt.Errorf("not an offer token")
	}
	uidF, ok := claims["user_id"].(float64)
	if !ok {
		return 0, "", fmt.Errorf("no user_id")
	}
	kind, _ := claims["kind"].(string)
	return uint(uidF), kind, nil
}

// ensureStripeCustomer returns the user's Stripe customer id, creating one if
// this is their first checkout (abandoners never created one).
func ensureStripeCustomer(user *User) (string, error) {
	if user.StripeCustomerID != "" {
		return user.StripeCustomerID, nil
	}
	stripe.Key = getEnv("STRIPE_SECRET_KEY", "")
	cus, err := customer.New(&stripe.CustomerParams{Email: stripe.String(user.Email)})
	if err != nil {
		return "", err
	}
	user.StripeCustomerID = cus.ID
	db.Save(user)
	return cus.ID, nil
}

// offerCheckoutHandler (public GET /offer/:token) starts a discounted Stripe
// Checkout for the user named in the token, then redirects to Stripe. Any
// failure falls back to the normal upgrade page rather than showing an error.
func offerCheckoutHandler(c *gin.Context) {
	fallback := offerBaseURL() + "/upgrade"
	uid, kind, err := parseOfferToken(c.Param("token"))
	if err != nil {
		c.Redirect(http.StatusFound, fallback)
		return
	}
	var user User
	if err := db.First(&user, uid).Error; err != nil {
		c.Redirect(http.StatusFound, fallback)
		return
	}

	priceID := getEnv("STRIPE_PRICE_PREMIUM", getEnv("STRIPE_PRICE_ID", ""))
	if priceID == "" {
		c.Redirect(http.StatusFound, fallback)
		return
	}
	customerID, err := ensureStripeCustomer(&user)
	if err != nil {
		c.Redirect(http.StatusFound, fallback)
		return
	}
	stripe.Key = getEnv("STRIPE_SECRET_KEY", "")
	params := &stripe.CheckoutSessionParams{
		Customer:           stripe.String(customerID),
		PaymentMethodTypes: stripe.StringSlice([]string{"card"}),
		Mode:               stripe.String(string(stripe.CheckoutSessionModeSubscription)),
		LineItems: []*stripe.CheckoutSessionLineItemParams{
			{Price: stripe.String(priceID), Quantity: stripe.Int64(1)},
		},
		SuccessURL: stripe.String(getEnv("STRIPE_SUCCESS_URL", offerBaseURL()+"/thank-you-page")),
		CancelURL:  stripe.String(getEnv("STRIPE_CANCEL_URL", offerBaseURL()+"/cancel")),
	}
	meta := map[string]string{"user_id": strconv.FormatUint(uint64(uid), 10), "offer": kind}
	params.Metadata = meta
	params.SubscriptionData = &stripe.CheckoutSessionSubscriptionDataParams{Metadata: meta}
	s, err := session.New(params)
	if err != nil {
		log.Printf("❌ offer checkout for user %d: %v", uid, err)
		c.Redirect(http.StatusFound, fallback)
		return
	}
	c.Redirect(http.StatusFound, s.URL)
}

// offerUnsubscribeHandler (public GET /offer/unsubscribe/:token) opts the user
// out of promotional email (CAN-SPAM).
func offerUnsubscribeHandler(c *gin.Context) {
	uid, _, err := parseOfferToken(c.Param("token"))
	if err != nil {
		c.String(http.StatusBadRequest, "This unsubscribe link is invalid or has expired.")
		return
	}
	db.Model(&User{}).Where("id = ?", uid).Update("marketing_opt_out", true)
	c.String(http.StatusOK, "You've been unsubscribed from Narrafied promotional emails. You'll still receive essential account emails.")
}

// adminOfferTestHandler (admin GET /admin/offer-test?kind=abandon|winback&to=…)
// sends one real offer email using the caller's own account, so the full
// email→/offer/ checkout path can be verified without enabling the mass loop.
func adminOfferTestHandler(c *gin.Context) {
	claims, _ := c.Get("claims")
	uid := uint(claims.(jwt.MapClaims)["user_id"].(float64))
	var u User
	if err := db.First(&u, uid).Error; err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "user not found"})
		return
	}
	kind := c.DefaultQuery("kind", "abandon")
	if kind != "winback" {
		kind = "abandon"
	}
	to := c.DefaultQuery("to", u.Email)
	tok, err := offerToken(uid, kind)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	subject, html, text := offerContent(kind,
		offerBaseURL()+"/offer/"+tok, offerBaseURL()+"/unsubscribe/"+tok)
	if err := sendEmail(to, subject, html, text); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{"sent": true, "to": to, "kind": kind, "email_configured": emailConfigured()})
}

// offerEmailLoop scans hourly for eligible users. Disabled unless
// OFFER_EMAILS_ENABLED=true, so nothing sends until deliberately switched on.
func offerEmailLoop() {
	if os.Getenv("OFFER_EMAILS_ENABLED") != "true" {
		log.Printf("ℹ️ offer email loop disabled (set OFFER_EMAILS_ENABLED=true to enable)")
		return
	}
	log.Printf("✅ offer email loop enabled (hourly)")
	// small delay so migrations/boot settle before the first pass
	time.Sleep(60 * time.Second)
	for {
		runOfferEmailPass()
		time.Sleep(1 * time.Hour)
	}
}

func runOfferEmailPass() {
	now := time.Now()
	old := now.Add(-72 * time.Hour) // don't reach back further than 3 days
	ready := now.Add(-24 * time.Hour)

	// Abandonment: registered, still free, 24–72h old, not referral-premium.
	var abandoners []User
	db.Where("account_type = ? AND email <> '' AND created_at < ? AND created_at > ?", "free", ready, old).
		Where("(marketing_opt_out IS NULL OR marketing_opt_out = false)").
		Where("(premium_until IS NULL OR premium_until < ?)", now).
		Where("id NOT IN (SELECT user_id FROM offer_emails WHERE kind = 'abandon')").
		Limit(200).Find(&abandoners)
	for i := range abandoners {
		sendOffer(&abandoners[i], "abandon")
	}

	// Winback: canceled 24–72h ago and now free.
	var churned []User
	db.Where("account_type = ? AND email <> '' AND subscription_canceled_at IS NOT NULL AND subscription_canceled_at < ? AND subscription_canceled_at > ?", "free", ready, old).
		Where("(marketing_opt_out IS NULL OR marketing_opt_out = false)").
		Where("id NOT IN (SELECT user_id FROM offer_emails WHERE kind = 'winback')").
		Limit(200).Find(&churned)
	for i := range churned {
		sendOffer(&churned[i], "winback")
	}

	if len(abandoners)+len(churned) > 0 {
		log.Printf("📧 offer pass: %d abandon, %d winback", len(abandoners), len(churned))
	}
}

func sendOffer(u *User, kind string) {
	tok, err := offerToken(u.ID, kind)
	if err != nil {
		log.Printf("❌ offer token for user %d: %v", u.ID, err)
		return
	}
	link := offerBaseURL() + "/offer/" + tok
	unsub := offerBaseURL() + "/unsubscribe/" + tok
	subject, html, text := offerContent(kind, link, unsub)

	if err := sendEmail(u.Email, subject, html, text); err != nil {
		log.Printf("❌ offer email to %s: %v", u.Email, err)
		return // don't record, so it retries next pass
	}
	db.Create(&OfferEmail{UserID: u.ID, Kind: kind, SentAt: time.Now()})
}

func offerContent(kind, link, unsub string) (subject, html, text string) {
	discount := discountDisplay()
	anchor := anchorDisplay()
	if kind == "winback" {
		subject = "Come back to Narrafied — " + discount + "/month"
		text = fmt.Sprintf(
			"We saved your library.\n\n"+
				"Your audiobooks are right where you left them. Restart your Narrafied "+
				"subscription at %s/month — our standard rate, below the %s you were on.\n\n"+
				"Resume here: %s\n\n"+
				"— The Narrafied team\n\nUnsubscribe: %s",
			discount, anchor, link, unsub)
	} else {
		subject = "Your Narrafied audiobooks are waiting — " + discount + "/month"
		text = fmt.Sprintf(
			"Turn any book into a cinematic audiobook.\n\n"+
				"You signed up but haven't started a plan yet. Here's our standard rate, "+
				"%s/month (less than the %s on the signup screen) — unlimited replays of "+
				"everything you create.\n\n"+
				"Start listening: %s\n\n"+
				"— The Narrafied team\n\nUnsubscribe: %s",
			discount, anchor, link, unsub)
	}
	html = offerHTML(kind, discount, anchor, link, unsub)
	return
}

func offerHTML(kind, discount, anchor, link, unsub string) string {
	var headline, body, cta string
	if kind == "winback" {
		headline = "We saved your library."
		body = fmt.Sprintf("Your audiobooks are right where you left them. Restart your subscription at <strong>%s/month</strong> — our standard rate, below the %s you were on.", discount, anchor)
		cta = "Resume my subscription"
	} else {
		headline = "Your audiobooks are waiting."
		body = fmt.Sprintf("You signed up but haven't picked a plan yet. Here's our standard rate, <strong>%s/month</strong> — less than the %s on the signup screen, with unlimited replays of everything you create.", discount, anchor)
		cta = "Start listening"
	}
	return fmt.Sprintf(`<!doctype html><html><body style="margin:0;background:#0f120c;font-family:-apple-system,Segoe UI,Roboto,sans-serif;">
<div style="max-width:520px;margin:0 auto;padding:40px 28px;color:#eef1e7;">
  <div style="font-size:22px;font-weight:800;color:#43c579;margin-bottom:28px;">Narrafied</div>
  <h1 style="font-size:26px;line-height:1.2;margin:0 0 14px;">%s</h1>
  <p style="font-size:16px;line-height:1.6;color:#c7cebc;margin:0 0 28px;">%s</p>
  <a href="%s" style="display:inline-block;background:#1f9d57;color:#fff;text-decoration:none;font-weight:700;font-size:16px;padding:14px 28px;border-radius:10px;">%s</a>
  <p style="font-size:13px;color:#77826c;margin:36px 0 0;line-height:1.6;">
    Narrafied · AI-narrated audiobooks<br>
    <a href="%s" style="color:#77826c;">Unsubscribe from promotional emails</a>
  </p>
</div></body></html>`, headline, body, link, cta, unsub)
}
