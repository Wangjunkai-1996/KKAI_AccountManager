// tools/openai-login/cmd/main.go
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"math/rand"
	"os"
	"strings"
	"time"

	"github.com/Wei-Shaw/sub2api/tools/openai-login/internal/exporter"
	"github.com/Wei-Shaw/sub2api/tools/openai-login/internal/login"
)

func main() {
	// 命令行参数
	inputFile := flag.String("input", "accounts.txt", "输入文件（每行用 --- 或 ---- 分隔 email、password、totp_secret）")
	outputFile := flag.String("output", "sub2api-accounts.json", "输出文件")
	headless := flag.Bool("headless", false, "无头模式（默认关闭，避免被 Cloudflare 拦截）")
	proxy := flag.String("proxy", "", "代理地址（可选，格式：http://host:port）")
	retryCount := flag.Int("retry", 2, "失败重试次数")
	delayMin := flag.Int("delay-min", 5, "账号处理间隔最小值（秒）")
	delayMax := flag.Int("delay-max", 10, "账号处理间隔最大值（秒）")
	timeout := flag.Int("timeout", 60, "单个账号超时时间（秒）")
	flag.Parse()

	rand.Seed(time.Now().UnixNano())
	if err := login.ValidateHTTPProxy(*proxy); err != nil {
		log.Fatalf("❌ 代理配置无效: %v", err)
	}

	printBanner()

	log.Printf("📄 输入文件: %s", *inputFile)
	log.Printf("📦 输出文件: %s", *outputFile)
	log.Printf("🎭 无头模式: %v", *headless)
	log.Printf("🌐 代理: %s", getProxyDisplay(*proxy))
	log.Printf("🔄 重试次数: %d", *retryCount)
	log.Printf("⏱️  超时时间: %d 秒", *timeout)
	log.Printf("⏳ 处理间隔: %d-%d 秒", *delayMin, *delayMax)

	// 读取账号列表
	credentials, err := readCredentialsFromFile(*inputFile)
	if err != nil {
		log.Fatalf("❌ 读取账号文件失败: %v", err)
	}

	log.Printf("\n📊 共读取 %d 个账号\n", len(credentials))

	if len(credentials) == 0 {
		log.Fatal("❌ 没有可处理的账号")
	}

	// 创建登录服务
	service := login.NewService(login.Config{
		Headless:   *headless,
		Proxy:      *proxy,
		RetryCount: *retryCount,
		Timeout:    time.Duration(*timeout) * time.Second,
	})

	// 批量处理账号
	results := make([]*exporter.AccountResult, 0, len(credentials))
	successCount := 0
	failCount := 0

	startTime := time.Now()

	for i, cred := range credentials {
		log.Printf("\n%s", strings.Repeat("=", 70))
		log.Printf("[%d/%d] 🔐 处理账号: %s", i+1, len(credentials), cred.Email)
		log.Printf("%s", strings.Repeat("=", 70))

		// 验证 TOTP 密钥格式
		if !login.ValidateTOTPSecret(cred.TOTPSecret) {
			log.Printf("❌ TOTP 密钥格式错误，跳过该账号")
			failCount++
			results = append(results, &exporter.AccountResult{
				Email:   cred.Email,
				Success: false,
				Error:   "Invalid TOTP secret format",
			})
			continue
		}

		// 登录并获取 token
		result, err := service.Login(cred.Email, cred.Password, cred.TOTPSecret)
		if err != nil {
			log.Printf("❌ 登录失败: %v", err)
			failCount++

			// 记录失败信息
			results = append(results, &exporter.AccountResult{
				Email:   cred.Email,
				Success: false,
				Error:   err.Error(),
			})
			continue
		}

		log.Printf("✅ 登录成功！")
		log.Printf("   📧 邮箱: %s", result.Email)
		// Tokens are credentials; never print them (even partially) to logs.
		log.Printf("   🔑 Access Token: 已获取")
		log.Printf("   🔄 Refresh Token: 已获取")
		log.Printf("   🆔 Account ID: %s", result.ChatGPTAccountID)
		log.Printf("   🏢 Organization ID: %s", result.OrganizationID)
		log.Printf("   📦 Plan Type: %s", result.PlanType)
		log.Printf("   ⏰ Expires At: %s", time.Unix(result.ExpiresAt, 0).Format(time.RFC3339))
		log.Printf("   ⏱️  Expires In: %d seconds (%.1f days)", result.ExpiresIn, float64(result.ExpiresIn)/86400)

		successCount++

		// 保存结果
		results = append(results, &exporter.AccountResult{
			Email:            cred.Email,
			Password:         cred.Password,
			TOTPSecret:       cred.TOTPSecret,
			AccessToken:      result.AccessToken,
			RefreshToken:     result.RefreshToken,
			ChatGPTAccountID: result.ChatGPTAccountID,
			OrganizationID:   result.OrganizationID,
			PlanType:         result.PlanType,
			ExpiresAt:        result.ExpiresAt,
			ExpiresIn:        result.ExpiresIn,
			Success:          true,
		})

		// 延时避免触发限制
		if i < len(credentials)-1 {
			delay := *delayMin + rand.Intn(*delayMax-*delayMin+1)
			log.Printf("\n⏳ 等待 %d 秒后处理下一个账号...", delay)
			time.Sleep(time.Duration(delay) * time.Second)
		}
	}

	elapsed := time.Since(startTime)

	// 打印统计信息
	printSummary(successCount, failCount, len(credentials), elapsed)

	if successCount == 0 {
		log.Fatal("❌ 没有成功的账号，不生成 JSON 文件")
	}

	// 生成 sub2api JSON
	sub2apiData := exporter.GenerateSub2APIJson(results)

	// 打印导出摘要
	log.Printf("\n%s", exporter.GenerateSummary(sub2apiData))

	// 保存到文件
	jsonData, err := json.MarshalIndent(sub2apiData, "", "  ")
	if err != nil {
		log.Fatalf("❌ 生成 JSON 失败: %v", err)
	}

	if err := os.WriteFile(*outputFile, jsonData, 0600); err != nil {
		log.Fatalf("❌ 保存文件失败: %v", err)
	}

	log.Printf("\n✅ JSON 文件已保存: %s", *outputFile)
	log.Printf("📁 文件大小: %.2f KB", float64(len(jsonData))/1024)
	log.Printf("\n%s", getImportInstructions(*outputFile))
}

type Credential struct {
	Email      string
	Password   string
	TOTPSecret string
}

func readCredentialsFromFile(filename string) ([]Credential, error) {
	data, err := os.ReadFile(filename)
	if err != nil {
		return nil, err
	}

	lines := strings.Split(string(data), "\n")
	credentials := make([]Credential, 0)

	for i, line := range lines {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}

		parts := strings.Split(line, "----")
		if len(parts) != 3 {
			parts = strings.Split(line, "---")
		}
		if len(parts) != 3 {
			log.Printf("⚠️  跳过第 %d 行：格式错误（需要用 --- 或 ---- 分隔 email、password、totp_secret）", i+1)
			continue
		}

		credentials = append(credentials, Credential{
			Email:      strings.TrimSpace(parts[0]),
			Password:   strings.TrimSpace(parts[1]),
			TOTPSecret: strings.TrimSpace(parts[2]),
		})
	}

	return credentials, nil
}

func printBanner() {
	banner := `
╔══════════════════════════════════════════════════════════════════╗
║                                                                  ║
║              🤖 OpenAI 自动登录工具 v1.0                         ║
║                                                                  ║
║              为 sub2api 项目自动获取 access_token                ║
║                                                                  ║
╚══════════════════════════════════════════════════════════════════╝
`
	fmt.Println(banner)
}

func printSummary(success, fail, total int, elapsed time.Duration) {
	summary := fmt.Sprintf(`
╔══════════════════════════════════════════════════════════════════╗
║                           处理完成                                ║
╠══════════════════════════════════════════════════════════════════╣
║  ✅ 成功: %-54d ║
║  ❌ 失败: %-54d ║
║  📊 总计: %-54d ║
║  ⏱️  耗时: %-54s ║
║  📈 成功率: %-51.1f%% ║
╚══════════════════════════════════════════════════════════════════╝
`, success, fail, total, elapsed.Round(time.Second), float64(success)/float64(total)*100)

	log.Print(summary)
}

func getProxyDisplay(proxy string) string {
	if proxy == "" {
		return "无"
	}
	return "已配置 HTTP 代理"
}

func getImportInstructions(filename string) string {
	return fmt.Sprintf(`
╔══════════════════════════════════════════════════════════════════╗
║                        导入到 sub2api                             ║
╠══════════════════════════════════════════════════════════════════╣
║                                                                  ║
║  方法 1：通过管理后台导入                                         ║
║  1. 登录 sub2api 管理后台                                        ║
║  2. 进入"账号管理"页面                                            ║
║  3. 点击"导入"按钮                                                ║
║  4. 选择文件: %s                            ║
║                                                                  ║
║  方法 2：通过 API 导入                                            ║
║  curl -X POST http://your-sub2api:8080/api/v1/admin/accounts/import \
║       -H "Content-Type: application/json" \                      ║
║       -H "Authorization: Bearer YOUR_TOKEN" \                    ║
║       -d @%s                                    ║
║                                                                  ║
╚══════════════════════════════════════════════════════════════════╝
`, filename, filename)
}
