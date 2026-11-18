package handlers

// WebhookErrorRuleOf returns the key, refusal flag and max of the rule matching err.
func WebhookErrorRuleOf(err error) (key string, refusal bool, maxLen int) {
	r := lookupWebhookError(err)
	return r.key, r.refusal, r.max
}

// WebhookErrorRuleCount returns how many rules the webhook error table holds.
func WebhookErrorRuleCount() int { return len(webhookErrorRules()) }

// IsSignInRefusal reports whether a sign-in failure answers the 403 refusal page rather than the 500 unavailable page.
func IsSignInRefusal(err error) bool { return isRefusal(err) }

// QuickstartSnippets renders the quickstart send and verify snippets for base and device.
func QuickstartSnippets(base, device string) (send, verify map[string]string, payload string, err error) { //nolint:nonamedreturns // three results differ in role
	s, v, p, err := quickstartSnippets(base, device)
	send, verify = map[string]string{}, map[string]string{}
	for _, x := range s {
		send[x.Lang] = x.Code
	}
	for _, x := range v {
		verify[x.Lang] = x.Code
	}
	return send, verify, p, err
}
