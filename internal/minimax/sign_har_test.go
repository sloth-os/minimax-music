package minimax

import "testing"

// TestSign_RealTraffic verifies the `yy` signature reproduces the EXACT values
// captured in www.minimaxi.com2.har, for both REST and the music WebSocket.
// These cases lock the algorithm (and the query parameter insertion order) to
// real accepted traffic — not just internal consistency. If sign.go or
// buildCommonParams changes in a way that alters the signature, this fails.
//
// Each case is: method, hasSearchParamsPath (path+"?"+<query in browser order>,
// excluding yy/token/op_ticket), body (raw POST bytes, "{}"/"" for GET/WS),
// unix (ms), and the yy actually captured on the wire.
func TestSign_RealTraffic(t *testing.T) {
	cases := []struct {
		name   string
		method string
		path   string // hasSearchParamsPath: path+"?"+query
		body   string // "{}" for GET/WS, raw POST body for POST
		unix   int64
		want   string // captured yy
	}{
		{
			"GET model_info (REST, with device_id)",
			"GET",
			"/v1/music/model_info?device_platform=web&app_id=3001&version_code=22201&biz_id=1&uuid=0043e8d9-d2e2-4450-830f-f6d0a35f5f10&lang=zh-Hans&device_id=547946206444445699&os_name=Windows&browser_name=edge&device_memory=16&cpu_core_num=16&browser_language=en-US&browser_platform=Win32&screen_width=1707&screen_height=1067&unix=1787496731000",
			"{}",
			1787496731000,
			"d49c465978aaf4d0f80fc4f03d3445f4",
		},
		{
			"GET common_config (REST, extra filter param, no device_id)",
			"GET",
			"/v1/api/config/web/common_config?filter=t2a_input_config%2Cvoice_tag_language%2Cvoice_tag_gender%2Cvoice_tag_age%2Cvoice_tag_accent%2Cdefault_selected_voice%2Cpay_white_list%2Ct2a_model%2Cmusic_model%2Chome_show_cases%2Cmusic_limit%2Cvoice_constants_map%2Cnotification_live_modal_config%2Chome_config%2Clyrics_commands%2Cmusic_style_sug_list%2Cmusic_random_style_prompts%2Cmusic_quantity_list%2Calipay_white_list%2Ct2v_gender%2Ct2v_language%2Cvoice_design_demo%2Cwechat_group_list%2Cvoice_category_options%2Cvoice_recommended_filters%2Cactivity_config%2Chome_banners%2Cnew_feature_modal_config%2Cnew_model_tooltip_config%2Cnew_tts_model_tooltip_config%2Cprivilege_comparison_config%2Ctutorial_config%2Ccycle_tab_tag_config%2CcoverModelFreeEndTime%2CfreeMusicModelsList&device_platform=web&app_id=3001&version_code=22201&biz_id=1&uuid=0043e8d9-d2e2-4450-830f-f6d0a35f5f10&lang=zh-Hans&os_name=Windows&browser_name=edge&device_memory=16&cpu_core_num=16&browser_language=en-US&browser_platform=Win32&screen_width=1707&screen_height=1067&unix=1787496700000",
			"{}",
			1787496700000,
			"33805d556f9bf955e408b553339ebc17",
		},
		{
			"POST device/register (REST POST with body)",
			"POST",
			"/v1/api/user/device/register?device_platform=web&app_id=3001&version_code=22201&biz_id=1&uuid=0043e8d9-d2e2-4450-830f-f6d0a35f5f10&lang=zh-Hans&os_name=Windows&browser_name=edge&device_memory=16&cpu_core_num=16&browser_language=en-US&browser_platform=Win32&screen_width=1707&screen_height=1067&unix=1787496700000",
			`{"uuid":"0043e8d9-d2e2-4450-830f-f6d0a35f5f10"}`,
			1787496700000,
			"62dff67b137232da3c303653fc0e33d1",
		},
		{
			"GET music/ws (WebSocket query signature)",
			"GET",
			"/v1/api/music/ws?device_platform=web&app_id=3001&version_code=22201&biz_id=1&uuid=0043e8d9-d2e2-4450-830f-f6d0a35f5f10&lang=zh-Hans&device_id=547946206444445699&os_name=Windows&browser_name=edge&device_memory=16&cpu_core_num=16&browser_language=en-US&browser_platform=Win32&screen_width=1707&screen_height=1067&unix=1787496922433",
			"{}",
			1787496922433,
			"36806132754677a81e7a393d39bd83cc",
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var body []byte
			if c.method == "POST" {
				body = []byte(c.body)
			}
			got := sign(c.path, c.method, body, c.unix)
			if got != c.want {
				t.Fatalf("sign: got %s, want %s (captured from HAR)", got, c.want)
			}
		})
	}
}

// TestBuildCommonParams_Order verifies the common params are emitted in the
// browser's insertion order (device_id between lang and os_name), not
// alphabetically sorted. This is what makes signatures match captured traffic.
func TestBuildCommonParams_Order(t *testing.T) {
	c := &Client{cfg: Config{
		AppID: "3001", VersionCode: "22201", BizID: "1",
		UUID: "uuid-x", DeviceID: "dev-1", Lang: "zh-Hans",
		OSName: "Windows", BrowserName: "edge",
		DeviceMemory: 16, CPUCoreNum: 16,
		BrowserLanguage: "en-US", BrowserPlatform: "Win32",
		ScreenWidth: 1707, ScreenHeight: 1067,
	}}
	params := c.buildCommonParams(1787496731000)
	wantOrder := []string{
		"device_platform", "app_id", "version_code", "biz_id",
		"uuid", "lang", "device_id", "os_name", "browser_name",
		"device_memory", "cpu_core_num", "browser_language",
		"browser_platform", "screen_width", "screen_height", "unix",
	}
	if len(params) != len(wantOrder) {
		t.Fatalf("got %d params, want %d", len(params), len(wantOrder))
	}
	for i, k := range wantOrder {
		if params[i].Key != k {
			t.Fatalf("param[%d] = %q, want %q (order matters for signature)", i, params[i].Key, k)
		}
	}
	// device_id must be absent when not configured (browser omits it entirely
	// before device/register returns one).
	c.cfg.DeviceID = ""
	params = c.buildCommonParams(1787496731000)
	for _, p := range params {
		if p.Key == "device_id" {
			t.Fatalf("device_id should be omitted when unset, got %q", p.Value)
		}
	}
}
