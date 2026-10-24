package openapi

import (
	"fmt"
	"math"
	"strconv"
	"unicode"
)

// evaluateExpression 计算衍生指标表达式，支持 + - * /、括号、数字与源指标编码。
// 不引入第三方 eval，避免任意代码执行；未知的指标编码返回错误。
func evaluateExpression(expr string, values map[string]float64) (float64, error) {
	p := &exprParser{input: expr, values: values}
	value, err := p.parseExpr()
	if err != nil {
		return 0, err
	}
	p.skipSpace()
	if p.pos < len(p.input) {
		return 0, fmt.Errorf("表达式在位置 %d 存在无法解析的内容", p.pos)
	}
	if math.IsNaN(value) || math.IsInf(value, 0) {
		return 0, fmt.Errorf("计算结果非有限数值")
	}
	return value, nil
}

type exprParser struct {
	input  string
	pos    int
	values map[string]float64
}

func (p *exprParser) parseExpr() (float64, error) {
	left, err := p.parseTerm()
	if err != nil {
		return 0, err
	}
	for {
		p.skipSpace()
		if p.pos >= len(p.input) {
			return left, nil
		}
		op := p.input[p.pos]
		if op != '+' && op != '-' {
			return left, nil
		}
		p.pos++
		right, err := p.parseTerm()
		if err != nil {
			return 0, err
		}
		if op == '+' {
			left += right
		} else {
			left -= right
		}
	}
}

func (p *exprParser) parseTerm() (float64, error) {
	left, err := p.parseFactor()
	if err != nil {
		return 0, err
	}
	for {
		p.skipSpace()
		if p.pos >= len(p.input) {
			return left, nil
		}
		op := p.input[p.pos]
		if op != '*' && op != '/' {
			return left, nil
		}
		p.pos++
		right, err := p.parseFactor()
		if err != nil {
			return 0, err
		}
		if op == '*' {
			left *= right
		} else {
			if right == 0 {
				return 0, fmt.Errorf("除数为 0")
			}
			left /= right
		}
	}
}

func (p *exprParser) parseFactor() (float64, error) {
	p.skipSpace()
	if p.pos >= len(p.input) {
		return 0, fmt.Errorf("表达式意外结束")
	}
	c := p.input[p.pos]
	if c == '(' {
		p.pos++
		value, err := p.parseExpr()
		if err != nil {
			return 0, err
		}
		p.skipSpace()
		if p.pos >= len(p.input) || p.input[p.pos] != ')' {
			return 0, fmt.Errorf("缺少右括号")
		}
		p.pos++
		return value, nil
	}
	if c == '-' {
		p.pos++
		value, err := p.parseFactor()
		return -value, err
	}
	if c == '+' {
		p.pos++
		return p.parseFactor()
	}
	if unicode.IsDigit(rune(c)) || c == '.' {
		start := p.pos
		for p.pos < len(p.input) && (unicode.IsDigit(rune(p.input[p.pos])) || p.input[p.pos] == '.') {
			p.pos++
		}
		value, err := strconv.ParseFloat(p.input[start:p.pos], 64)
		if err != nil {
			return 0, fmt.Errorf("数字 %q 非法", p.input[start:p.pos])
		}
		return value, nil
	}
	if isIdentStart(c) {
		start := p.pos
		for p.pos < len(p.input) && isIdentPart(p.input[p.pos]) {
			p.pos++
		}
		name := p.input[start:p.pos]
		if value, ok := p.values[name]; ok {
			return value, nil
		}
		return 0, fmt.Errorf("源指标 %q 没有可用数据", name)
	}
	return 0, fmt.Errorf("位置 %d 出现非法字符 %q", p.pos, string(c))
}

func (p *exprParser) skipSpace() {
	for p.pos < len(p.input) && unicode.IsSpace(rune(p.input[p.pos])) {
		p.pos++
	}
}

func isIdentStart(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isIdentPart(c byte) bool {
	return isIdentStart(c) || (c >= '0' && c <= '9') || c == '.'
}
