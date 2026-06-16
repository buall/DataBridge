package fakedata

import (
	"fmt"
	"math/rand"
	"strings"
	"time"
)

type UserRow struct {
	Email      string
	FullName   string
	City       string
	Company    string
	JobTitle   string
	Status     string
	Score      int
	Phone      string
	CreatedAt  time.Time
}

type OrderRow struct {
	OrderNo    string
	Customer   string
	City       string
	Status     string
	Channel    string
	Amount     float64
	CreatedAt  time.Time
}

type ProductRow struct {
	SKU        string
	Name       string
	Category   string
	Brand      string
	Price      float64
	Stock      int
	Status     string
	CreatedAt  time.Time
}

var firstNames = []string{
	"Olivia", "Liam", "Emma", "Noah", "Ava", "Ethan", "Sophia", "Lucas", "Mia", "James",
	"Yuki", "Satoshi", "Wei", "Jia", "Arjun", "Aditi", "Kai", "Hannah", "Evelyn", "Daniel",
}

var lastNames = []string{
	"Chen", "Tan", "Lim", "Wang", "Zhang", "Lee", "Smith", "Johnson", "Garcia", "Brown",
	"Patel", "Kim", "Ng", "Liu", "Miller", "Davis", "Wilson", "Taylor", "Anderson", "Thomas",
}

var cities = []string{
	"Singapore", "Shanghai", "Shenzhen", "Tokyo", "Frankfurt", "San Jose", "Sydney", "Toronto",
}

var companies = []string{
	"Acme Cloud", "Northwind Data", "BlueRiver Labs", "Aster Works", "Helio Commerce", "CityOps",
	"Granite Systems", "Pinecone Retail", "Nimbus Finance", "Orbit Analytics",
}

var titles = []string{
	"Product Manager", "Data Analyst", "Backend Engineer", "Operations Lead", "Sales Director",
	"Support Specialist", "Finance Manager", "Growth Manager", "QA Engineer", "Customer Success Manager",
}

var statuses = []string{"active", "trial", "pending", "disabled"}
var orderStatuses = []string{"paid", "pending", "cancelled", "refunded"}
var channels = []string{"web", "app", "partner", "offline"}
var productCategories = []string{"Electronics", "Home", "Beauty", "Fashion", "Sports"}
var productBrands = []string{"Aster", "Northwind", "Nimbus", "Helio", "BlueRiver"}

func GenerateUsers(rows int) []UserRow {
	if rows <= 0 {
		rows = 100
	}
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	result := make([]UserRow, 0, rows)
	for i := 0; i < rows; i++ {
		first := firstNames[rng.Intn(len(firstNames))]
		last := lastNames[rng.Intn(len(lastNames))]
		fullName := first + " " + last
		email := fmt.Sprintf("%s.%s%d@databridge-demo.com",
			strings.ToLower(first),
			strings.ToLower(last),
			rng.Intn(900)+100,
		)
		phone := fmt.Sprintf("+65 8%03d %04d", rng.Intn(1000), rng.Intn(10000))
		result = append(result, UserRow{
			Email:     email,
			FullName:  fullName,
			City:      cities[rng.Intn(len(cities))],
			Company:   companies[rng.Intn(len(companies))],
			JobTitle:  titles[rng.Intn(len(titles))],
			Status:    statuses[rng.Intn(len(statuses))],
			Score:     50 + rng.Intn(51),
			Phone:     phone,
			CreatedAt: time.Now().Add(-time.Duration(rng.Intn(180*24)) * time.Hour),
		})
	}
	return result
}

func GenerateOrders(rows int) []OrderRow {
	if rows <= 0 {
		rows = 100
	}
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	result := make([]OrderRow, 0, rows)
	for i := 0; i < rows; i++ {
		first := firstNames[rng.Intn(len(firstNames))]
		last := lastNames[rng.Intn(len(lastNames))]
		result = append(result, OrderRow{
			OrderNo:   fmt.Sprintf("SO-%06d", 100000+rng.Intn(900000)),
			Customer:  first + " " + last,
			City:      cities[rng.Intn(len(cities))],
			Status:    orderStatuses[rng.Intn(len(orderStatuses))],
			Channel:   channels[rng.Intn(len(channels))],
			Amount:    float64(50+rng.Intn(5000)) / 10,
			CreatedAt: time.Now().Add(-time.Duration(rng.Intn(180*24)) * time.Hour),
		})
	}
	return result
}

func GenerateProducts(rows int) []ProductRow {
	if rows <= 0 {
		rows = 100
	}
	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	result := make([]ProductRow, 0, rows)
	for i := 0; i < rows; i++ {
		category := productCategories[rng.Intn(len(productCategories))]
		brand := productBrands[rng.Intn(len(productBrands))]
		name := fmt.Sprintf("%s %s %d", brand, category, rng.Intn(900)+100)
		result = append(result, ProductRow{
			SKU:       fmt.Sprintf("SKU-%05d", 10000+rng.Intn(90000)),
			Name:      name,
			Category:  category,
			Brand:     brand,
			Price:     float64(100+rng.Intn(20000)) / 100,
			Stock:     rng.Intn(500),
			Status:    []string{"active", "draft", "archived"}[rng.Intn(3)],
			CreatedAt: time.Now().Add(-time.Duration(rng.Intn(365*24)) * time.Hour),
		})
	}
	return result
}
