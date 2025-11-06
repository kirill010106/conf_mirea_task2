package main

import (
	"encoding/csv"
	"fmt"
	"os"
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
		if version == "" {
			errors = append(errors, "version не может быть пустым")
		} else {
			config.Version = version
		}
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

func (c *Config) Print() {
	fmt.Println("=== Конфигурация приложения ===")
	fmt.Printf("package_name: %s\n", c.PackageName)
	fmt.Printf("repository_url: %s\n", c.RepositoryURL)
	fmt.Printf("test_mode: %t\n", c.TestMode)
	fmt.Printf("version: %s\n", c.Version)
	fmt.Printf("max_depth: %d\n", c.MaxDepth)
	fmt.Println("================================")
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

	config.Print()

	fmt.Println("\nПриложение успешно инициализировано!")
}
