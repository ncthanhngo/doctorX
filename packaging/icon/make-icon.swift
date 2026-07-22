// Vẽ icon ứng dụng DoctorX bằng CoreGraphics rồi xuất PNG ở mọi kích thước cho
// iconset. Vector hoá bằng đường vẽ nên sắc nét ở cả 16px lẫn 1024px, và dùng
// đúng bảng màu thương hiệu của app (teal → cyan, chữ thập cứu hộ trắng).
//
//   swift make-icon.swift <thư-mục-xuất>
//
// Xuất các file icon_16x16.png ... icon_512x512@2x.png vào thư mục đó, sẵn sàng
// cho `iconutil -c icns`.
import AppKit

let outDir = CommandLine.arguments.count > 1 ? CommandLine.arguments[1] : "."

// Màu thương hiệu (khớp Theme.swift).
func color(_ hex: UInt32) -> CGColor {
    CGColor(red: CGFloat((hex >> 16) & 0xFF) / 255,
            green: CGFloat((hex >> 8) & 0xFF) / 255,
            blue: CGFloat(hex & 0xFF) / 255, alpha: 1)
}
let teal = color(0x0D9488)
let cyan = color(0x0891B2)
let tealDeep = color(0x0F766E)

// Vẽ icon vào một context kích thước s×s.
func drawIcon(_ ctx: CGContext, _ s: CGFloat) {
    ctx.setAllowsAntialiasing(true)
    ctx.interpolationQuality = .high

    // 1. Nền squircle bo góc kiểu macOS, full-bleed.
    let radius = s * 0.2237
    let bg = CGPath(roundedRect: CGRect(x: 0, y: 0, width: s, height: s),
                    cornerWidth: radius, cornerHeight: radius, transform: nil)
    ctx.saveGState()
    ctx.addPath(bg)
    ctx.clip()
    // Gradient teal → cyan theo đường chéo.
    let space = CGColorSpaceCreateDeviceRGB()
    let grad = CGGradient(colorsSpace: space, colors: [tealDeep, teal, cyan] as CFArray,
                          locations: [0, 0.5, 1])!
    ctx.drawLinearGradient(grad, start: CGPoint(x: 0, y: s),
                           end: CGPoint(x: s, y: 0), options: [])

    // Điểm sáng nhẹ ở góc trên cho cảm giác khối, hiện đại.
    let hi = CGGradient(colorsSpace: space,
                        colors: [CGColor(gray: 1, alpha: 0.22), CGColor(gray: 1, alpha: 0)] as CFArray,
                        locations: [0, 1])!
    ctx.drawRadialGradient(hi, startCenter: CGPoint(x: s * 0.3, y: s * 0.8), startRadius: 0,
                           endCenter: CGPoint(x: s * 0.3, y: s * 0.8), endRadius: s * 0.7, options: [])
    ctx.restoreGState()

    // 2. Vòng "quét" mảnh phía sau chữ thập — gợi ý chức năng scan.
    ctx.saveGState()
    ctx.setStrokeColor(CGColor(gray: 1, alpha: 0.18))
    ctx.setLineWidth(s * 0.028)
    let ringR = s * 0.315
    ctx.addEllipse(in: CGRect(x: s/2 - ringR, y: s/2 - ringR, width: ringR*2, height: ringR*2))
    ctx.strokePath()
    ctx.restoreGState()

    // 3. Chữ thập cứu hộ trắng, bo góc, có bóng đổ mềm.
    let arm = s * 0.15      // nửa bề rộng thanh
    let reach = s * 0.30    // nửa chiều dài thanh
    let cr = arm * 0.42     // bo góc
    let cx = s / 2, cy = s / 2

    func crossPath() -> CGPath {
        let p = CGMutablePath()
        p.addRoundedRect(in: CGRect(x: cx - arm, y: cy - reach, width: arm*2, height: reach*2),
                         cornerWidth: cr, cornerHeight: cr)   // thanh dọc
        p.addRoundedRect(in: CGRect(x: cx - reach, y: cy - arm, width: reach*2, height: arm*2),
                         cornerWidth: cr, cornerHeight: cr)   // thanh ngang
        return p
    }

    ctx.saveGState()
    ctx.setShadow(offset: CGSize(width: 0, height: -s * 0.012), blur: s * 0.03,
                  color: CGColor(gray: 0, alpha: 0.22))
    ctx.addPath(crossPath())
    ctx.setFillColor(CGColor(gray: 1, alpha: 1))
    ctx.fillPath()
    ctx.restoreGState()

    // Chuyển sắc rất nhẹ trên chữ thập cho đỡ phẳng.
    ctx.saveGState()
    ctx.addPath(crossPath())
    ctx.clip()
    let crossGrad = CGGradient(colorsSpace: space,
                               colors: [CGColor(gray: 1, alpha: 1), color(0xE6FBF8)] as CFArray,
                               locations: [0, 1])!
    ctx.drawLinearGradient(crossGrad, start: CGPoint(x: cx, y: cy + reach),
                           end: CGPoint(x: cx, y: cy - reach), options: [])
    ctx.restoreGState()
}

func renderPNG(size: Int) -> Data {
    let s = CGFloat(size)
    let rep = NSBitmapImageRep(bitmapDataPlanes: nil, pixelsWide: size, pixelsHigh: size,
                              bitsPerSample: 8, samplesPerPixel: 4, hasAlpha: true,
                              isPlanar: false, colorSpaceName: .deviceRGB,
                              bytesPerRow: 0, bitsPerPixel: 0)!
    NSGraphicsContext.saveGraphicsState()
    let gctx = NSGraphicsContext(bitmapImageRep: rep)!
    NSGraphicsContext.current = gctx
    drawIcon(gctx.cgContext, s)
    gctx.flushGraphics()
    NSGraphicsContext.restoreGraphicsState()
    return rep.representation(using: .png, properties: [:])!
}

// Các kích thước iconset chuẩn của macOS.
let specs: [(name: String, px: Int)] = [
    ("icon_16x16", 16), ("icon_16x16@2x", 32),
    ("icon_32x32", 32), ("icon_32x32@2x", 64),
    ("icon_128x128", 128), ("icon_128x128@2x", 256),
    ("icon_256x256", 256), ("icon_256x256@2x", 512),
    ("icon_512x512", 512), ("icon_512x512@2x", 1024),
]
for spec in specs {
    let data = renderPNG(size: spec.px)
    let url = URL(fileURLWithPath: outDir).appendingPathComponent("\(spec.name).png")
    try! data.write(to: url)
    print("→ \(spec.name).png (\(spec.px)px)")
}
