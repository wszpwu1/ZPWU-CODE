# Generates PWA icons using System.Drawing (Windows). Idempotent.
param(
  [string]$OutDir = (Join-Path (Split-Path -Parent $PSScriptRoot) "web\icons")
)
Add-Type -AssemblyName System.Drawing
New-Item -ItemType Directory -Force -Path $OutDir | Out-Null

function New-Icon {
  param([int]$Size, [string]$File, [bool]$Maskable)
  $bmp = New-Object System.Drawing.Bitmap $Size, $Size
  $g = [System.Drawing.Graphics]::FromImage($bmp)
  $g.SmoothingMode = [System.Drawing.Drawing2D.SmoothingMode]::AntiAlias
  $g.InterpolationMode = [System.Drawing.Drawing2D.InterpolationMode]::HighQualityBicubic
  $g.TextRenderingHint = [System.Drawing.Text.TextRenderingHint]::AntiAlias
  $g.Clear([System.Drawing.Color]::FromArgb(11,16,32))

  if ($Maskable) { $padRatio = 0.15 } else { $padRatio = 0.06 }
  $pad = [int]([math]::Round($Size * $padRatio))
  $side = $Size - 2 * $pad
  $rect = New-Object System.Drawing.Rectangle $pad, $pad, $side, $side

  $radius = [int]([math]::Round($side * 0.18))
  $d = $radius * 2
  $path = New-Object System.Drawing.Drawing2D.GraphicsPath
  $path.AddArc($rect.X, $rect.Y, $d, $d, 180, 90)
  $path.AddArc($rect.Right - $d, $rect.Y, $d, $d, 270, 90)
  $path.AddArc($rect.Right - $d, $rect.Bottom - $d, $d, $d, 0, 90)
  $path.AddArc($rect.X, $rect.Bottom - $d, $d, $d, 90, 90)
  $path.CloseFigure()

  $brush = New-Object System.Drawing.Drawing2D.LinearGradientBrush( `
      $rect, `
      [System.Drawing.Color]::FromArgb(59,130,246), `
      [System.Drawing.Color]::FromArgb(147,51,234), 45)
  $g.FillPath($brush, $path)

  $fontSize = [int]([math]::Round($Size * 0.55))
  $font = New-Object System.Drawing.Font('Segoe UI', $fontSize, [System.Drawing.FontStyle]::Bold, [System.Drawing.GraphicsUnit]::Pixel)
  $fmt = New-Object System.Drawing.StringFormat
  $fmt.Alignment = [System.Drawing.StringAlignment]::Center
  $fmt.LineAlignment = [System.Drawing.StringAlignment]::Center
  $textRect = New-Object System.Drawing.RectangleF 0, ([single](-$Size*0.02)), ([single]$Size), ([single]$Size)
  $g.DrawString('Z', $font, [System.Drawing.Brushes]::White, $textRect, $fmt)

  $g.Dispose()
  $path.Dispose()
  $brush.Dispose()
  $font.Dispose()
  $fullPath = [System.IO.Path]::GetFullPath($File)
  $bmp.Save($fullPath, [System.Drawing.Imaging.ImageFormat]::Png)
  $bmp.Dispose()
  Write-Host "wrote $fullPath"
}

New-Icon -Size 192 -File (Join-Path $OutDir 'icon-192.png') -Maskable $false
New-Icon -Size 512 -File (Join-Path $OutDir 'icon-512.png') -Maskable $false
New-Icon -Size 512 -File (Join-Path $OutDir 'maskable-icon-512.png') -Maskable $true
New-Icon -Size 180 -File (Join-Path $OutDir 'apple-touch-icon.png') -Maskable $false
