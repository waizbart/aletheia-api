package main

const (
	ThresholdL1Fast = 0.98
	ThresholdL4Fast = 0.97
	ThresholdL4Full = 0.97

	ThresholdDinoGlobal   = 0.90
	ThresholdDinoLocalAgg = 0.90
	ThresholdDinoLocalMin = 0.90

	DinoInputSize     = 728 // múltiplo de 14
	dinoPatchGrid     = 52  // 728 / 14
	dinoNumPatches    = dinoPatchGrid * dinoPatchGrid
	dinoNumTokens     = dinoNumPatches + 1
	dinoHiddenDim     = 384
	DinoLocalGridSize = 8
	dinoLocalCells    = DinoLocalGridSize * DinoLocalGridSize
	dinoLocalBits     = dinoLocalCells * dinoHiddenDim

	ColorGridSize      = 4
	ColorHistBins      = 32
	ColorPreprocessMax = 256 // reduz custo do L4 antes dos histogramas
	colorTotalBins     = ColorGridSize * ColorGridSize * 6 * ColorHistBins

	RotNetInputSize = 224

	// Tiling DINO: máximo de tiles (rows*cols) e workers ONNX paralelos.
	DinoMaxTiles   = 16
	DinoMaxWorkers = 8
)
