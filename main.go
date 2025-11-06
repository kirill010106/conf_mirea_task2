package main

import (
	"bufio"
	"compress/gzip"
	"encoding/csv"
	"fmt"
	"io"
	"net/http"
	"os"
	"regexp"
	"strconv"
	"strings"
)

// Config структура для хранения настроек приложения
type Config struct {
	PackageName   string // Имя анализируемого пакета
	RepositoryURL string // URL-адрес репозитория или путь к файлу тестового репозитория
	TestMode      bool   // Режим работы с тестовым репозиторием
	Version       string // Версия пакета
	MaxDepth      int    // Максимальная глубина анализа зависимостей
}

// Package представляет информацию о пакете Ubuntu
type Package struct {
	Name         string
	Version      string
	Dependencies []string
}

// Node представляет узел в графе зависимостей
type Node struct {
	Name         string
	Version      string
	Dependencies []string
	Depth        int
}

// Graph представляет граф зависимостей
type Graph struct {
	Nodes         map[string]*Node // Карта пакетов (имя -> узел)
	Edges         map[string][]string // Рёбра графа (имя -> список зависимостей)
	Cycles        []string // Обнаруженные циклы
	MaxDepth      int
	PackageSource map[string][]Package // Кэш всех пакетов для быстрого поиска
}

// StackItem представляет элемент стека для итеративного DFS
type StackItem struct {
	PackageName string
	Depth       int
	Path        []string // Путь для обнаружения циклов
}

func LoadConfig(filename string) (*Config, error) {
	// Проверка существования файла
	if _, err := os.Stat(filename); os.IsNotExist(err) {
		return nil, fmt.Errorf("файл конфигурации не найден: %s", filename)
	}

	// Открытие файла
	file, err := os.Open(filename)
	if err != nil {
		return nil, fmt.Errorf("ошибка открытия файла: %v", err)
	}
	defer file.Close()

	// Чтение CSV
	reader := csv.NewReader(file)
	reader.TrimLeadingSpace = true

	records, err := reader.ReadAll()
	if err != nil {
		return nil, fmt.Errorf("ошибка чтения CSV: %v", err)
	}

	if len(records) == 0 {
		return nil, fmt.Errorf("файл конфигурации пуст")
	}

	configMap := make(map[string]string)
	for i, record := range records {
		if len(record) < 2 {
			return nil, fmt.Errorf("неверный формат в строке %d: недостаточно столбцов", i+1)
		}
		key := strings.TrimSpace(record[0])
		value := strings.TrimSpace(record[1])

		if key == "" {
			return nil, fmt.Errorf("пустой ключ в строке %d", i+1)
		}

		configMap[key] = value
	}

	config := &Config{}

	if err := validateAndSetConfig(config, configMap); err != nil {
		return nil, err
	}

	return config, nil
}

func validateAndSetConfig(config *Config, configMap map[string]string) error {
	var errors []string

	if packageName, ok := configMap["package_name"]; ok {
		if packageName == "" {
			errors = append(errors, "package_name не может быть пустым")
		} else {
			config.PackageName = packageName
		}
	} else {
		errors = append(errors, "обязательный параметр package_name отсутствует")
	}

	if repoURL, ok := configMap["repository_url"]; ok {
		if repoURL == "" {
			errors = append(errors, "repository_url не может быть пустым")
		} else {
			config.RepositoryURL = repoURL
		}
	} else {
		errors = append(errors, "обязательный параметр repository_url отсутствует")
	}

	if testModeStr, ok := configMap["test_mode"]; ok {
		testMode, err := strconv.ParseBool(testModeStr)
		if err != nil {
			errors = append(errors, fmt.Sprintf("неверное значение test_mode: %s (ожидается true/false)", testModeStr))
		} else {
			config.TestMode = testMode
		}
	} else {
		errors = append(errors, "обязательный параметр test_mode отсутствует")
	}

	if version, ok := configMap["version"]; ok {
		config.Version = version // Версия может быть пустой для поиска последней версии
	} else {
		errors = append(errors, "обязательный параметр version отсутствует")
	}

	if maxDepthStr, ok := configMap["max_depth"]; ok {
		maxDepth, err := strconv.Atoi(maxDepthStr)
		if err != nil {
			errors = append(errors, fmt.Sprintf("неверное значение max_depth: %s (ожидается целое число)", maxDepthStr))
		} else if maxDepth < 1 {
			errors = append(errors, fmt.Sprintf("max_depth должен быть больше 0, получено: %d", maxDepth))
		} else if maxDepth > 100 {
			errors = append(errors, fmt.Sprintf("max_depth слишком велик (максимум 100), получено: %d", maxDepth))
		} else {
			config.MaxDepth = maxDepth
		}
	} else {
		errors = append(errors, "обязательный параметр max_depth отсутствует")
	}

	if len(errors) > 0 {
		return fmt.Errorf("ошибки валидации конфигурации:\n  - %s", strings.Join(errors, "\n  - "))
	}

	return nil
}

// fetchPackagesFile загружает файл Packages из репозитория Ubuntu
func fetchPackagesFile(repoURL string, testMode bool) (io.Reader, error) {
	if testMode {
		// В тестовом режиме читаем из локального файла
		file, err := os.Open(repoURL)
		if err != nil {
			return nil, fmt.Errorf("ошибка открытия локального файла: %v", err)
		}
		return file, nil
	}

	// Загружаем из интернета
	resp, err := http.Get(repoURL)
	if err != nil {
		return nil, fmt.Errorf("ошибка загрузки файла: %v", err)
	}

	if resp.StatusCode != http.StatusOK {
		resp.Body.Close()
		return nil, fmt.Errorf("ошибка HTTP: статус %d", resp.StatusCode)
	}

	// Проверяем, является ли файл сжатым
	if strings.HasSuffix(repoURL, ".gz") {
		gzReader, err := gzip.NewReader(resp.Body)
		if err != nil {
			resp.Body.Close()
			return nil, fmt.Errorf("ошибка распаковки gzip: %v", err)
		}
		return gzReader, nil
	}

	return resp.Body, nil
}

// parsePackagesFile парсит файл Packages формата Debian
func parsePackagesFile(reader io.Reader) ([]Package, error) {
	var packages []Package
	scanner := bufio.NewScanner(reader)

	var currentPkg Package
	var inPackage bool

	for scanner.Scan() {
		line := scanner.Text()

		// Пустая строка означает конец записи о пакете
		if line == "" {
			if inPackage && currentPkg.Name != "" {
				packages = append(packages, currentPkg)
				currentPkg = Package{}
				inPackage = false
			}
			continue
		}

		// Пропускаем продолжения строк (начинаются с пробела)
		if strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
			continue
		}

		// Парсим поля
		parts := strings.SplitN(line, ":", 2)
		if len(parts) != 2 {
			continue
		}

		field := strings.TrimSpace(parts[0])
		value := strings.TrimSpace(parts[1])

		switch field {
		case "Package":
			inPackage = true
			currentPkg.Name = value
		case "Version":
			currentPkg.Version = value
		case "Depends":
			currentPkg.Dependencies = parseDependencies(value)
		}
	}

	// Добавляем последний пакет, если файл не заканчивается пустой строкой
	if inPackage && currentPkg.Name != "" {
		packages = append(packages, currentPkg)
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("ошибка чтения файла: %v", err)
	}

	return packages, nil
}

// parseDependencies парсит строку зависимостей и извлекает имена пакетов
func parseDependencies(depString string) []string {
	var deps []string

	// Регулярное выражение для извлечения имени пакета (до версии или альтернативы)
	// Формат: package-name (>= version) | alternative, another-package
	// Поддерживаем как маленькие, так и заглавные буквы (для тестовых графов)
	re := regexp.MustCompile(`([a-zA-Z0-9][a-zA-Z0-9+\-.]*)`)

	// Разделяем по запятой (разные зависимости)
	parts := strings.Split(depString, ",")

	for _, part := range parts {
		part = strings.TrimSpace(part)

		// Берем первую альтернативу (до |)
		alternatives := strings.Split(part, "|")
		if len(alternatives) > 0 {
			firstAlt := strings.TrimSpace(alternatives[0])

			// Извлекаем имя пакета (до пробела, скобки или конца строки)
			matches := re.FindStringSubmatch(firstAlt)
			if len(matches) > 0 {
				pkgName := matches[1]
				// Исключаем виртуальные пакеты и специальные символы
				if pkgName != "" && !strings.Contains(pkgName, "$") {
					deps = append(deps, pkgName)
				}
			}
		}
	}

	return deps
}

// findPackage ищет пакет по имени и версии
func findPackage(packages []Package, name, version string) (*Package, error) {
	var candidates []Package

	// Сначала ищем точное совпадение по версии
	for _, pkg := range packages {
		if pkg.Name == name {
			if version == "" || pkg.Version == version {
				return &pkg, nil
			}
			candidates = append(candidates, pkg)
		}
	}

	// Если точного совпадения нет, но есть кандидаты с другими версиями
	if len(candidates) > 0 {
		// Возвращаем первый найденный (обычно самая новая версия идет первой)
		fmt.Printf("Внимание: пакет %s версии %s не найден, используется версия %s\n",
			name, version, candidates[0].Version)
		return &candidates[0], nil
	}

	return nil, fmt.Errorf("пакет %s не найден", name)
}

// getDirectDependencies получает прямые зависимости пакета
func getDirectDependencies(config *Config) ([]string, error) {
	fmt.Println("\n=== Получение зависимостей ===")
	fmt.Printf("Загрузка данных из: %s\n", config.RepositoryURL)

	// Загружаем файл Packages
	reader, err := fetchPackagesFile(config.RepositoryURL, config.TestMode)
	if err != nil {
		return nil, err
	}

	// Закрываем reader, если это Closer
	if closer, ok := reader.(io.Closer); ok {
		defer closer.Close()
	}

	fmt.Println("Парсинг данных о пакетах...")

	// Парсим файл
	packages, err := parsePackagesFile(reader)
	if err != nil {
		return nil, err
	}

	fmt.Printf("Найдено пакетов: %d\n", len(packages))
	fmt.Printf("Поиск пакета: %s (версия: %s)\n", config.PackageName, config.Version)

	// Ищем нужный пакет
	pkg, err := findPackage(packages, config.PackageName, config.Version)
	if err != nil {
		return nil, err
	}

	fmt.Printf("Пакет найден: %s (%s)\n", pkg.Name, pkg.Version)

	return pkg.Dependencies, nil
}

// buildDependencyGraph строит граф зависимостей используя итеративный DFS (без рекурсии)
func buildDependencyGraph(config *Config) (*Graph, error) {
	fmt.Println("\n=== Построение графа зависимостей ===")
	fmt.Printf("Загрузка данных из: %s\n", config.RepositoryURL)
	
	// Загружаем файл Packages
	reader, err := fetchPackagesFile(config.RepositoryURL, config.TestMode)
	if err != nil {
		return nil, err
	}
	
	// Закрываем reader, если это Closer
	if closer, ok := reader.(io.Closer); ok {
		defer closer.Close()
	}
	
	fmt.Println("Парсинг данных о пакетах...")
	
	// Парсим файл
	packages, err := parsePackagesFile(reader)
	if err != nil {
		return nil, err
	}
	
	fmt.Printf("Найдено пакетов: %d\n", len(packages))
	
	// Создаём индекс пакетов для быстрого поиска
	packageMap := make(map[string][]Package)
	for _, pkg := range packages {
		packageMap[pkg.Name] = append(packageMap[pkg.Name], pkg)
	}
	
	// Инициализируем граф
	graph := &Graph{
		Nodes:         make(map[string]*Node),
		Edges:         make(map[string][]string),
		Cycles:        []string{},
		MaxDepth:      config.MaxDepth,
		PackageSource: packageMap,
	}
	
	// Итеративный DFS с использованием стека
	fmt.Printf("\nЗапуск DFS для пакета: %s (max_depth: %d)\n", config.PackageName, config.MaxDepth)
	
	stack := []StackItem{{
		PackageName: config.PackageName,
		Depth:       0,
		Path:        []string{},
	}}
	
	visited := make(map[string]bool)       // Полностью обработанные узлы
	inProgress := make(map[string]bool)    // Узлы в процессе обработки (для обнаружения циклов)
	
	for len(stack) > 0 {
		// Берём элемент из стека
		item := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		
		pkgName := item.PackageName
		depth := item.Depth
		path := item.Path
		
		// Проверка на цикл
		cycleDetected := false
		for _, p := range path {
			if p == pkgName {
				cycleStr := strings.Join(append(path, pkgName), " -> ")
				// Добавляем цикл только если его еще нет
				found := false
				for _, existingCycle := range graph.Cycles {
					if existingCycle == cycleStr {
						found = true
						break
					}
				}
				if !found {
					graph.Cycles = append(graph.Cycles, cycleStr)
					fmt.Printf("  [!] Обнаружен цикл: %s\n", cycleStr)
				}
				cycleDetected = true
				break
			}
		}
		
		// Пропускаем узел, если обнаружен цикл
		if cycleDetected {
			continue
		}
		
		// Пропускаем, если уже посещали
		if visited[pkgName] {
			continue
		}
		
		// Проверяем глубину
		if depth > config.MaxDepth {
			continue
		}
		
		// Ищем пакет
		pkgList, exists := packageMap[pkgName]
		if !exists || len(pkgList) == 0 {
			// Пакет не найден, добавляем узел без зависимостей
			if _, nodeExists := graph.Nodes[pkgName]; !nodeExists {
				graph.Nodes[pkgName] = &Node{
					Name:         pkgName,
					Version:      "unknown",
					Dependencies: []string{},
					Depth:        depth,
				}
			}
			visited[pkgName] = true
			continue
		}
		
		// Берём первый найденный пакет (или с нужной версией)
		var pkg Package
		if config.Version != "" && pkgName == config.PackageName {
			found := false
			for _, p := range pkgList {
				if p.Version == config.Version {
					pkg = p
					found = true
					break
				}
			}
			if !found {
				pkg = pkgList[0]
			}
		} else {
			pkg = pkgList[0]
		}
		
		// Добавляем узел в граф
		if _, exists := graph.Nodes[pkgName]; !exists {
			graph.Nodes[pkgName] = &Node{
				Name:         pkg.Name,
				Version:      pkg.Version,
				Dependencies: pkg.Dependencies,
				Depth:        depth,
			}
			graph.Edges[pkgName] = pkg.Dependencies
		}
		
		visited[pkgName] = true
		inProgress[pkgName] = true
		
		// Добавляем зависимости в стек (если не превышена глубина)
		if depth < config.MaxDepth {
			newPath := append([]string{}, path...)
			newPath = append(newPath, pkgName)
			
			for _, dep := range pkg.Dependencies {
				// Проверяем, создает ли эта зависимость цикл
				createsCycle := false
				for _, p := range newPath {
					if p == dep {
						cycleStr := strings.Join(append(newPath, dep), " -> ")
						// Проверяем, не добавляли ли мы уже этот цикл
						found := false
						for _, existingCycle := range graph.Cycles {
							if existingCycle == cycleStr {
								found = true
								break
							}
						}
						if !found {
							graph.Cycles = append(graph.Cycles, cycleStr)
							fmt.Printf("  [!] Обнаружен цикл: %s\n", cycleStr)
						}
						createsCycle = true
						break
					}
				}
				
				if !createsCycle && !visited[dep] {
					stack = append(stack, StackItem{
						PackageName: dep,
						Depth:       depth + 1,
						Path:        newPath,
					})
				}
			}
		}
		
		inProgress[pkgName] = false
	}
	
	fmt.Printf("\nГраф построен:\n")
	fmt.Printf("  - Узлов: %d\n", len(graph.Nodes))
	fmt.Printf("  - Рёбер: %d\n", len(graph.Edges))
	fmt.Printf("  - Обнаружено циклов: %d\n", len(graph.Cycles))
	
	return graph, nil
}

// printGraph выводит граф зависимостей в удобочитаемом виде
func printGraph(graph *Graph, rootPackage string) {
	fmt.Println("\n=== Граф зависимостей ===")
	
	// Рекурсивная печать дерева
	printed := make(map[string]bool)
	printNode(graph, rootPackage, 0, printed)
	
	// Выводим информацию о циклах
	if len(graph.Cycles) > 0 {
		fmt.Println("\n=== Обнаруженные циклы ===")
		for i, cycle := range graph.Cycles {
			fmt.Printf("%d. %s\n", i+1, cycle)
		}
	}
}

// printNode рекурсивно выводит узел и его зависимости
func printNode(graph *Graph, pkgName string, indent int, printed map[string]bool) {
	prefix := strings.Repeat("  ", indent)
	
	node, exists := graph.Nodes[pkgName]
	if !exists {
		fmt.Printf("%s- %s (не найден)\n", prefix, pkgName)
		return
	}
	
	// Проверяем, был ли узел уже напечатан (для избежания бесконечных циклов)
	if printed[pkgName] {
		fmt.Printf("%s- %s [%s] (depth: %d) [уже показан]\n", prefix, node.Name, node.Version, node.Depth)
		return
	}
	
	fmt.Printf("%s- %s [%s] (depth: %d)\n", prefix, node.Name, node.Version, node.Depth)
	printed[pkgName] = true
	
	// Печатаем зависимости
	if node.Depth < graph.MaxDepth {
		for _, dep := range node.Dependencies {
			printNode(graph, dep, indent+1, printed)
		}
	}
}

func main() {
	configFile := "config.csv"

	if len(os.Args) > 1 {
		configFile = os.Args[1]
	}

	config, err := LoadConfig(configFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Ошибка: %v\n", err)
		os.Exit(1)
	}

	// Строим полный граф зависимостей
	graph, err := buildDependencyGraph(config)
	if err != nil {
		fmt.Fprintf(os.Stderr, "\nОшибка построения графа: %v\n", err)
		os.Exit(1)
	}

	// Выводим граф
	printGraph(graph, config.PackageName)

	fmt.Println("\n=== Анализ завершен успешно! ===")
}
