const h = 24
const w = 9
const textMax = 115
const textW = 120
let gridH = h
let gridW = w
let gridTextMax = textMax
let gridTextW = textW
let scale = 1
let textColor = "#8b8d94"

let signColorAlpha = 0.35
let isDark = true

function lightMode() {
    isDark = !isDark
    if (isDark) {
        textColor = "#8b8d94"
        signColorAlpha = 0.35
        document.body.classList.remove('light-mode')
    } else {
        textColor = "#52525b"
        signColorAlpha = 0.2
        document.body.classList.add('light-mode')
    }
    // Redraw legend with new colors
    legend()
}

function fix_dpi(id) {
    let canvas = document.getElementById(id),
        dpi = window.devicePixelRatio;
    gridH = h * dpi.valueOf()
    gridW = w * dpi.valueOf()
    gridTextMax = textMax * dpi.valueOf()
    gridTextW = textW * dpi.valueOf()
    let style = {
        height() {
            return +getComputedStyle(canvas).getPropertyValue('height').slice(0,-2);
        },
        width() {
            return +getComputedStyle(canvas).getPropertyValue('width').slice(0,-2);
        }
    }
    canvas.setAttribute('width', style.width() * dpi);
    canvas.setAttribute('height', style.height() * dpi);
    scale = dpi.valueOf()
}

function legend() {
    const l = document.getElementById("legend")
    l.height = scale * h * 1.2
    const ctx = l.getContext('2d')

    // Clear canvas for redraw
    ctx.clearRect(0, 0, l.width, l.height)

    let offset = textW

    // Proposer - green gradient
    let grad = ctx.createLinearGradient(offset, 0, offset+gridW, gridH)
    grad.addColorStop(0, 'rgb(34, 197, 94)');
    grad.addColorStop(0.5, 'rgb(74, 222, 128)');
    grad.addColorStop(1, 'rgb(34, 197, 94)');
    ctx.fillStyle = grad
    ctx.fillRect(offset, 0, gridW, gridH)
    ctx.font = `500 ${scale * 11}px monospace`
    ctx.fillStyle = textColor
    offset += gridW + gridW/2
    ctx.fillText("PROPOSER", offset, gridH/1.3)

    // Signed - subtle dark
    offset += 75 * scale
    grad = ctx.createLinearGradient(offset, 0, offset+gridW, gridH)
    grad.addColorStop(0, `rgba(139, 141, 148, ${signColorAlpha})`);
    grad.addColorStop(1, `rgba(139, 141, 148, ${signColorAlpha - 0.1})`);
    ctx.fillStyle = grad
    ctx.fillRect(offset, 0, gridW, gridH)
    ctx.fillStyle = textColor
    offset += gridW + gridW/2
    ctx.fillText("SIGNED", offset, gridH/1.3)

    // Miss precommit - blue
    offset += 55 * scale
    grad = ctx.createLinearGradient(offset, 0, offset+gridW, gridH)
    grad.addColorStop(0, '#60a5fa');
    grad.addColorStop(0.7, '#3b82f6');
    grad.addColorStop(1, '#1d4ed8');
    ctx.fillStyle = grad
    ctx.fillRect(offset, 0, gridW, gridH)
    offset += gridW + gridW/2
    ctx.fillStyle = textColor
    ctx.fillText("MISS/PRECOMMIT", offset, gridH/1.3)

    // Miss prevote - purple/pink
    offset += 115 * scale
    grad = ctx.createLinearGradient(offset, 0, offset+gridW, gridH)
    grad.addColorStop(0, '#a855f7');
    grad.addColorStop(0.5, '#c084fc');
    grad.addColorStop(1, '#a855f7');
    ctx.fillStyle = grad
    ctx.fillRect(offset, 0, gridW, gridH)
    offset += gridW + gridW/2
    ctx.fillStyle = textColor
    ctx.fillText("MISS/PREVOTE", offset, gridH/1.3)

    // Missed - orange/amber with strike
    offset += 100 * scale
    grad = ctx.createLinearGradient(offset, 0, offset+gridW, gridH)
    grad.addColorStop(0, '#f59e0b');
    grad.addColorStop(0.5, '#fbbf24');
    grad.addColorStop(1, '#d97706');
    ctx.fillStyle = grad
    ctx.fillRect(offset, 0, gridW, gridH)
    // Strike through line
    ctx.beginPath();
    ctx.moveTo(offset + 2, gridH/2);
    ctx.lineTo(offset + gridW - 2, gridH/2);
    ctx.closePath();
    ctx.strokeStyle = 'rgba(255,255,255,0.8)'
    ctx.lineWidth = 1.5
    ctx.stroke();
    offset += gridW + gridW/2
    ctx.fillStyle = textColor
    ctx.fillText("MISSED", offset, gridH/1.3)

    // No data - gray
    offset += 60 * scale
    grad = ctx.createLinearGradient(offset, 0, offset+gridW, gridH)
    grad.addColorStop(0, 'rgba(80, 82, 89, 0.4)');
    grad.addColorStop(1, 'rgba(80, 82, 89, 0.2)');
    ctx.fillStyle = grad
    ctx.fillRect(offset, 0, gridW, gridH)
    offset += gridW + gridW/2
    ctx.fillStyle = textColor
    ctx.fillText("NO DATA", offset, gridH/1.3)
}

function drawSeries(multiStates) {
    const canvas = document.getElementById("canvas")
    canvas.height = ((12*gridH*multiStates.Status.length)/10) + 30
    fix_dpi("canvas")
    if (canvas.getContext) {
        const ctx = canvas.getContext('2d')
        ctx.font = `500 ${scale * 13}px monospace`
        ctx.fillStyle = textColor

        let crossThrough = false
        for (let j = 0; j < multiStates.Status.length; j++) {

            // Chain name label
            ctx.fillStyle = textColor
            ctx.fillText(multiStates.Status[j].name, 5, (j*gridH)+(gridH*2)-6, gridTextMax)

            for (let i = 0; i < multiStates.Status[j].blocks.length; i++) {
                crossThrough = false
                const grad = ctx.createLinearGradient((i*gridW)+gridTextW, (gridH*j), (i * gridW) + gridW +gridTextW, (gridH*j))

                switch (multiStates.Status[j].blocks[i]) {
                    case 4: // proposed - green
                        grad.addColorStop(0, 'rgb(34, 197, 94)');
                        grad.addColorStop(0.5, 'rgb(74, 222, 128)');
                        grad.addColorStop(1, 'rgb(34, 197, 94)');
                        break
                    case 3: // signed - subtle alternating
                        if (j % 2 === 0) {
                            grad.addColorStop(0, `rgba(139, 141, 148, ${signColorAlpha})`);
                            grad.addColorStop(1, `rgba(139, 141, 148, ${signColorAlpha})`);
                        } else {
                            grad.addColorStop(0, `rgba(139, 141, 148, ${signColorAlpha - 0.15})`);
                            grad.addColorStop(1, `rgba(139, 141, 148, ${signColorAlpha - 0.15})`);
                        }
                        break
                    case 2: // precommit not included - blue
                        grad.addColorStop(0, '#60a5fa');
                        grad.addColorStop(0.7, '#3b82f6');
                        grad.addColorStop(1, '#1d4ed8');
                        break
                    case 1: // prevote not included - purple
                        grad.addColorStop(0, '#a855f7');
                        grad.addColorStop(0.5, '#c084fc');
                        grad.addColorStop(1, '#a855f7');
                        break
                    case 0: // missed - amber
                        grad.addColorStop(0, '#f59e0b');
                        grad.addColorStop(0.5, '#fbbf24');
                        grad.addColorStop(1, '#d97706');
                        crossThrough = true
                        break
                    default: // no data
                        grad.addColorStop(0, 'rgba(80, 82, 89, 0.3)');
                        grad.addColorStop(1, 'rgba(80, 82, 89, 0.15)');
                }
                ctx.clearRect((i*gridW)+gridTextW, gridH+(gridH*j), gridW, gridH)
                ctx.fillStyle = grad
                ctx.fillRect((i*gridW)+gridTextW, gridH+(gridH*j), gridW, gridH)

                // Subtle line between rows
                if (i > 0) {
                    ctx.beginPath();
                    ctx.moveTo((i * gridW) - gridW + gridTextW, 2 * gridH + (gridH * j) - 0.5)
                    ctx.lineTo((i * gridW) + gridTextW, 2 * gridH + (gridH * j) - 0.5);
                    ctx.closePath();
                    ctx.strokeStyle = 'rgba(255, 255, 255, 0.06)'
                    ctx.lineWidth = 1
                    ctx.stroke();
                }

                // Visual differentiation for missed blocks - white strike
                if (crossThrough) {
                    ctx.beginPath();
                    ctx.moveTo((i * gridW) + gridTextW + 2, (gridH*j) + (gridH * 2) - gridH / 2);
                    ctx.lineTo((i * gridW) + gridTextW + gridW - 2, (gridH*j) + (gridH * 2) - gridH / 2);
                    ctx.closePath();
                    ctx.strokeStyle = 'rgba(255, 255, 255, 0.8)'
                    ctx.lineWidth = 1.5
                    ctx.stroke();
                }
            }
        }
    }
}
