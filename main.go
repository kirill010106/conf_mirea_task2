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
	re := regexp.MustCompile(`([a-z0-9][a-z0-9+\-.]+)`)

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

func main() {
	configFile := "config.csv"

	if len(os.Args) > 1 {
		configFile = os.Args[1]
	}

	fmt.Printf("Загрузка конфигурации из файла: %s\n\n", configFile)

	config, err := LoadConfig(configFile)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Ошибка: %v\n", err)
		os.Exit(1)
	}
	fmt.Println("Конфигурация успешно загружена:")
	// Получаем прямые зависимости
	dependencies, err := getDirectDependencies(config)
	if err != nil {
		fmt.Fprintf(os.Stderr, "\nОшибка получения зависимостей: %v\n", err)
		os.Exit(1)
	}

	// Выводим прямые зависимости
	fmt.Println("\n=== Прямые зависимости ===")
	if len(dependencies) == 0 {
		fmt.Println("Зависимости отсутствуют")
	} else {
		fmt.Printf("Всего зависимостей: %d\n\n", len(dependencies))
		for i, dep := range dependencies {
			fmt.Printf("%d. %s\n", i+1, dep)
		}
	}

	fmt.Println("\n=== Анализ завершен успешно! ===")
}
