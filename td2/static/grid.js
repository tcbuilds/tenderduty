const BLOCK_WIDTH = 9
const BLOCK_HEIGHT = 24
const LABEL_WIDTH = 120

let isDark = true

// Cache previous block states to avoid unnecessary DOM updates
let previousStates = null

function lightMode() {
    isDark = !isDark
    if (isDark) {
        document.body.classList.remove('light-mode')
    } else {
        document.body.classList.add('light-mode')
    }
}

function getBlockClass(state, rowIndex) {
    switch (state) {
        case 4:
            return 'block-proposed'
        case 3:
            return rowIndex % 2 === 0 ? 'block-signed' : 'block-signed block-signed--alt'
        case 2:
            return 'block-precommit'
        case 1:
            return 'block-prevote'
        case 0:
            return 'block-missed'
        default:
            return 'block-nodata'
    }
}

function drawSeries(multiStates) {
    if (!multiStates.Status || multiStates.Status.length === 0) return

    const container = document.getElementById('blockGrid')
    if (!container) return

    const numChains = multiStates.Status.length
    const numBlocks = multiStates.Status[0].blocks.length

    // Check if we need to rebuild the entire grid or just update cells
    const needsRebuild = !previousStates
        || previousStates.length !== numChains
        || previousStates[0].blocks.length !== numBlocks

    if (needsRebuild) {
        buildGrid(container, multiStates, numChains, numBlocks)
    } else {
        updateGrid(container, multiStates, numChains, numBlocks)
    }

    // Store current states for diff comparison on next update
    previousStates = multiStates.Status.map(function(chain) {
        return {
            name: chain.name,
            blocks: chain.blocks.slice()
        }
    })
}

function buildGrid(container, multiStates, numChains, numBlocks) {
    const fragment = document.createDocumentFragment()
    const gridWidth = (numBlocks * BLOCK_WIDTH) + LABEL_WIDTH

    for (var j = 0; j < numChains; j++) {
        var row = document.createElement('div')
        row.className = 'block-row'
        row.style.width = gridWidth + 'px'

        var label = document.createElement('div')
        label.className = 'block-row__label'
        label.textContent = multiStates.Status[j].name
        label.style.width = LABEL_WIDTH + 'px'
        label.style.minWidth = LABEL_WIDTH + 'px'
        row.appendChild(label)

        var cellsContainer = document.createElement('div')
        cellsContainer.className = 'block-row__cells'
        cellsContainer.setAttribute('data-chain', j)

        for (var i = 0; i < numBlocks; i++) {
            var cell = document.createElement('div')
            cell.className = 'block-cell ' + getBlockClass(multiStates.Status[j].blocks[i], j)
            cell.setAttribute('data-idx', i)
            cellsContainer.appendChild(cell)
        }

        row.appendChild(cellsContainer)
        fragment.appendChild(row)
    }

    container.textContent = ''
    container.style.width = gridWidth + 'px'
    container.appendChild(fragment)
}

function updateGrid(container, multiStates, numChains, numBlocks) {
    var cellContainers = container.querySelectorAll('.block-row__cells')

    for (var j = 0; j < numChains; j++) {
        if (!cellContainers[j]) continue

        // Update label if chain name changed
        var label = cellContainers[j].parentElement.querySelector('.block-row__label')
        if (label && previousStates[j].name !== multiStates.Status[j].name) {
            label.textContent = multiStates.Status[j].name
        }

        var cells = cellContainers[j].children

        for (var i = 0; i < numBlocks; i++) {
            if (!cells[i]) continue

            // Only update if block state actually changed
            if (previousStates[j].blocks[i] !== multiStates.Status[j].blocks[i]) {
                cells[i].className = 'block-cell ' + getBlockClass(multiStates.Status[j].blocks[i], j)
            }
        }
    }
}

function legend() {
    var container = document.getElementById('legendItems')
    if (!container) return

    // Only build once - legend items are static
    if (container.children.length > 0) return

    var items = [
        { className: 'block-proposed', label: 'proposer' },
        { className: 'block-signed', label: 'signed' },
        { className: 'block-precommit', label: 'miss/precommit' },
        { className: 'block-prevote', label: 'miss/prevote' },
        { className: 'block-missed', label: 'missed' },
        { className: 'block-nodata', label: 'no data' }
    ]

    var fragment = document.createDocumentFragment()

    for (var k = 0; k < items.length; k++) {
        var item = document.createElement('div')
        item.className = 'legend-item'

        var swatch = document.createElement('div')
        swatch.className = 'legend-swatch ' + items[k].className

        var text = document.createElement('span')
        text.className = 'legend-text'
        text.textContent = items[k].label

        item.appendChild(swatch)
        item.appendChild(text)
        fragment.appendChild(item)
    }

    container.appendChild(fragment)
}
