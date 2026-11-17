package main

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"strelka/internal/ipc"
	"strelka/proto"
)

func placeLine(p *proto.Place) string {
	if p == nil {
		return "не известно"
	}
	city := p.City
	if city == "" {
		city = "без названия"
	}
	if p.Country != nil {
		city += " (" + *p.Country + ")"
	}
	how := "по IP"
	if p.Source == proto.PlaceManual {
		how = "выбрано вручную"
	}
	return fmt.Sprintf("%s, %.2f, %.2f — %s %s", city, p.Lat, p.Lon, how, p.At.Local().Format("02.01 15:04"))
}

func placeCmd(ctx context.Context, c *ipc.Client, args []string) error {
	if len(args) == 0 {
		st, err := getState(ctx, c)
		if err != nil {
			return err
		}
		fmt.Println(placeLine(st.Place))
		return nil
	}
	switch args[0] {
	case "detect":
		var p proto.Place
		if err := callLong(ctx, c, proto.MethodPlaceDetect, nil, &p); err != nil {
			return err
		}
		fmt.Println(placeLine(&p))
		return nil
	case "set":
		if len(args) < 4 || len(args) > 5 {
			return errors.New("нужно: place set ШИРОТА ДОЛГОТА ГОРОД [СТРАНА]")
		}
		lat, err1 := strconv.ParseFloat(strings.Replace(args[1], ",", ".", 1), 64)
		lon, err2 := strconv.ParseFloat(strings.Replace(args[2], ",", ".", 1), 64)
		if err1 != nil || err2 != nil {
			return errors.New("широта и долгота — числа, например 55.75 37.62")
		}
		params := proto.PlaceSetParams{Lat: lat, Lon: lon, City: args[3]}
		if len(args) == 5 {
			params.Country = &args[4]
		}
		if err := call(ctx, c, proto.MethodPlaceSet, params, nil); err != nil {
			return err
		}
		st, err := getState(ctx, c)
		if err != nil {
			return err
		}
		fmt.Println(placeLine(st.Place))
		return nil
	}
	return fmt.Errorf("не знаю «place %s»: есть detect и set", args[0])
}
